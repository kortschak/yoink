// Copyright ©2023 Dan Kortschak. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

// The yoink command extracts functions, types and other dependencies from
// a package for code copying reuse.
package main

//go:generate ./getupstream.bash

import (
	"bytes"
	"errors"
	"flag"
	"fmt"
	"go/ast"
	"go/format"
	"go/token"
	"go/types"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/kortschak/yoink/unused"
	"golang.org/x/tools/go/analysis"
	"golang.org/x/tools/go/ast/astutil"
	"golang.org/x/tools/go/packages"
	"golang.org/x/tools/txtar"
	"honnef.co/go/tools/analysis/facts/directives"
	"honnef.co/go/tools/analysis/facts/generated"
	"honnef.co/go/tools/analysis/lint"
)

// Exit status codes.
const (
	success       = 0
	internalError = 1 << (iota - 1)
	invocationError
)

func main() {
	os.Exit(Main())
}

const Usage = `
yoink is a code refactoring tool that can extract functions, types and other
identifier dependencies from a package for specialisation and reuse.
`

func Main() int {
	path := flag.String("pkg", "", "the source package (defaults to current directory)")
	dir := flag.String("dir", "", "destination directory for result (txtar on stdout if empty)")
	target := make(set)
	flag.Var(target, "y", "functions, types and other identifier declarations to extract\n(comma separated or multiple instances)")
	debug := flag.Bool("debug", false, "emit the use graph of the source package as DOT")
	flag.Usage = func() {
		fmt.Fprintf(flag.CommandLine.Output(), "Usage of %s:\n%s\n", os.Args[0], Usage)
		flag.PrintDefaults()
	}
	flag.Parse()
	if len(target) == 0 {
		fmt.Fprintln(os.Stderr, "missing targets")
		flag.Usage()
		os.Exit(invocationError)
	}
	return yoink(*path, target, *dir, *debug)
}

type set map[string]bool

func (s set) Set(v string) error {
	for _, y := range strings.Split(v, ",") {
		if y == "" {
			return errors.New("empty string target")
		}
		s[y] = true
	}
	return nil
}

func (s set) String() string {
	p := make([]string, 0, len(s))
	for y := range s {
		p = append(p, y)
	}
	sort.Strings(p)
	return strings.Join(p, ",")
}

// yoink extracts the target functions, types and other identifies from the
// package at pkgPath and prints the result to the directory dir. If dir
// is zero, a txtar representation of the source is written to stdout. If
// debug is true, a DOT representation of the use graph of the package is
// written to stdout.
func yoink(pkgPath string, target map[string]bool, dir string, debug bool) int {
	cfg := &packages.Config{
		Mode: packages.NeedFiles |
			packages.NeedImports |
			packages.NeedDeps |
			packages.NeedSyntax |
			packages.NeedTypes |
			packages.NeedTypesInfo |
			packages.NeedModule,
	}
	var pattern []string
	if pkgPath != "" {
		pattern = []string{pkgPath}
	}
	pkgs, err := packages.Load(cfg, pattern...)
	if err != nil {
		fmt.Fprintf(os.Stderr, "load: %v\n", err)
		return internalError
	}
	if packages.PrintErrors(pkgs) != 0 {
		return internalError
	}

	var ar []txtar.File
	for _, pkg := range pkgs { // pkgs should only ever be one long, ¯\_(ツ)_/¯.
		if debug {
			unused.Debug = os.Stderr
			unused.Run(&analysis.Pass{
				Fset:      pkg.Fset,
				Files:     pkg.Syntax,
				Pkg:       pkg.Types,
				TypesInfo: pkg.TypesInfo,
				ResultOf: map[*analysis.Analyzer]any{
					directives.Analyzer: []lint.Directive(nil),
					generated.Analyzer:  map[string]generated.Generator(nil),
				},
			})
			return success
		}

		// Calculate the use graph.
		nodes := unused.Graph(
			pkg.Fset,
			pkg.Syntax,
			pkg.Types,
			pkg.TypesInfo,
			[]lint.Directive(nil),
			map[string]generated.Generator(nil),
			unused.DefaultOptions,
		)

		// unused retains node positions as token.Position since
		// it makes sense there. It's less useful that way here, but
		// it's easier to just keep a table of token.Position for
		// when we need to look-up into the AST and type info than
		// to do re-writes on unused to bend it to our specific needs.
		// 🚫‍👀 🎁‍🐎 👄.
		table := make(map[token.Position]ast.Node)
		for _, f := range pkg.Syntax {
			ast.Inspect(f, func(n ast.Node) bool {
				if n == nil {
					return false
				}
				table[pkg.Fset.Position(n.Pos())] = n
				return true
			})
		}
		// Walk the use graph from all of our targets.
		seen := make(map[unused.NodeID]bool)
		for _, n := range nodes {
			if target[n.Obj.Name] {
				walk(nodes, n.ID, pkg.Fset, table, pkg.TypesInfo, seen, target)
			}
		}
		// Mark all seen nodes as wanted.
		want := make(map[token.Position]unused.Node)
		for n := range seen {
			want[nodes[n].Obj.Position] = nodes[n]
		}

		// Walk the AST to prune comments and methods from unwanted types.
		for _, f := range pkg.Syntax {
			cm := ast.NewCommentMap(pkg.Fset, f, f.Comments)
			for i := 0; i < len(f.Decls); {
				d := f.Decls[i]

				switch d := d.(type) {
				case *ast.GenDecl:
					for j := 0; j < len(d.Specs); {
						s := d.Specs[j]
						_, ok := want[pkg.Fset.Position(s.Pos())]
						if ok {
							j++
							continue
						}
						switch s := s.(type) {
						case *ast.ImportSpec:
							// Handle with after completion with goimports.
							j++
							continue
						case *ast.TypeSpec:
							// Remove methods for type.
							obj := pkg.TypesInfo.ObjectOf(s.Name)
							if obj != nil {
								switch o := obj.Type().(type) {
								case *types.Named:
									for i := 0; i < o.NumMethods(); i++ {
										delete(want, pkg.Fset.Position(o.Method(i).Pos()))
									}
								}
							}
						case *ast.ValueSpec:
						}
						deleteCommentsIn(d, cm)
						d.Specs = deleteNode(d.Specs, j)
					}
					if len(d.Specs) != 0 {
						i++
						continue
					}
					deleteCommentsIn(d, cm)
					f.Decls = deleteNode(f.Decls, i)

				case *ast.FuncDecl:
					i++
				default:
					panic(d)
				}
			}
			for i := 0; i < len(f.Decls); {
				d := f.Decls[i]

				switch d := d.(type) {
				case *ast.FuncDecl:
					_, ok := want[pkg.Fset.Position(d.Name.Pos())]
					if ok {
						i++
						continue
					}
					deleteCommentsIn(d, cm)
					f.Decls = deleteNode(f.Decls, i)

				case *ast.GenDecl:
					i++
				default:
					panic(d)
				}
			}

			if !needFile(f) {
				continue
			}

			// Re-write selectors for identifiers we did not want.
			astutil.Apply(f, func(c *astutil.Cursor) bool {
				switch n := c.Node().(type) {
				case *ast.Ident:
					switch c.Parent().(type) {
					case *ast.SelectorExpr, *ast.KeyValueExpr:
						return true
					}
					_, ok := want[pkg.Fset.Position(n.Pos())]
					if ok {
						break
					}
					o := pkg.TypesInfo.ObjectOf(n)
					if o == nil {
						break
					}
					if o.Type() == nil || !o.Exported() {
						break
					}
					_, ok = want[pkg.Fset.Position(o.Pos())]
					if ok {
						break
					}
					if o.Pkg() != nil {
						c.Replace(&ast.SelectorExpr{
							X: &ast.Ident{
								NamePos: n.Pos(),
								Name:    o.Pkg().Name(),
							},
							Sel: n,
						})
					}
				}
				return true
			}, nil)

			f.Comments = cm.Comments()

			var buf bytes.Buffer
			format.Node(&buf, pkg.Fset, f)

			filename := pkg.Fset.Position(f.FileStart).Filename
			file := filepath.Base(filename)
			dir := filepath.Base(filepath.Dir(filename))
			ar = append(ar, txtar.File{
				Name: filepath.Join(dir, file),
				Data: buf.Bytes(),
			})
		}
	}

	if dir != "" {
		fi, err := os.Stat(dir)
		if errors.Is(err, fs.ErrNotExist) {
			err = os.Mkdir(dir, 0o755)
			if err != nil {
				fmt.Fprintln(os.Stderr, err)
				return internalError
			}
		} else if !fi.IsDir() {
			fmt.Fprintf(os.Stderr, "%s exists and is not a directory", dir)
			return invocationError
		}
		for _, file := range ar {
			err = os.Mkdir(filepath.Join(dir, filepath.Dir(file.Name)), 0o755)
			if err != nil && !errors.Is(err, fs.ErrExist) {
				fmt.Fprintln(os.Stderr, err)
				return internalError
			}
			err = os.WriteFile(filepath.Join(dir, file.Name), file.Data, 0o644)
			if err != nil {
				fmt.Fprintln(os.Stderr, err)
				return internalError
			}
		}
	} else {
		os.Stdout.Write(txtar.Format(&txtar.Archive{Files: ar}))
	}
	return success
}

// deleteNode removes the ith ast.Node from s and returns the result.
func deleteNode[S []E, E ast.Node](s S, i int) S {
	n := append(s[:i], s[i+1:]...)
	var zero E
	s[len(s)-1] = zero
	return n
}

// deleteCommentsIn removes all comments in the AST sub-tree rooted at n
// from the provided ast.CommentMap.
func deleteCommentsIn(n ast.Node, from ast.CommentMap) {
	ast.Inspect(n, func(n ast.Node) bool {
		delete(from, n)
		return true
	})
}

// needFile returns whether f contains any declarations that are required
// to build the package.
func needFile(f *ast.File) bool {
	for _, d := range f.Decls {
		switch d := d.(type) {
		case *ast.GenDecl:
			for _, s := range d.Specs {
				switch s.(type) {
				case *ast.TypeSpec:
					return true
				case *ast.ValueSpec:
					return true
				}
			}
		case *ast.FuncDecl:
			return true
		}
	}
	return false
}

// walk does a depth-first traversal of the use graph in nodes, starting
// from n and marking visited nodes in seen. Only unexported identifiers or
// identifiers in targets are visited.
func walk(nodes []unused.Node, n unused.NodeID,
	fset *token.FileSet,
	table map[token.Position]ast.Node, info *types.Info,
	seen map[unused.NodeID]bool,
	targets map[string]bool,
) {
	if seen[n] {
		return
	}
	seen[n] = true

	for _, u := range nodes[n].Uses {
		id := table[nodes[u].Obj.Position].(*ast.Ident)
		if id.IsExported() && !targets[id.Name] {
			continue
		}
		if obj := info.ObjectOf(id); obj != nil {
			switch o := obj.Type().(type) {
			case *types.Named:
				for i := 0; i < o.NumMethods(); i++ {
					m := o.Method(i)
					for _, u := range nodes {
						if fset.Position(m.Pos()) == u.Obj.Position {
							walk(nodes, u.ID, fset, table, info, seen, targets)
						}
					}
				}
			}
		}
		walk(nodes, u, fset, table, info, seen, targets)
	}
}
