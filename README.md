# `yoink`

`yoink` is a code extraction refactoring tool. It can extract functions and types and their unexported dependencies from a package to simplify code copying reuse.

For example, to extract the `gzip.Writer` code into a new package.
```
$ yoink -pkg compress/gzip -y Writer,NewWriter,NewWriterLevel -dir .
$ goimports -w ./gzip
```

## Mechanism

The program determines a use graph of the package specified by the `-pkg` command line argument using a version of [honnef.co/go/tools/unused](https://pkg.go.dev/honnef.co/go/tools/unused) and traverses from the roots provided by the `-y` command line argument. Use traversal is blocked by exported indentifiers that are not included in the `-y` argument. When the graph has been walked, the set of wanted AST nodes is then used to prune the AST of the package to remove all code that is not required, and the result is printed.

## Caveats

Imports are not updated, so it will be necessary to run `goimports` on the resulting package.

Not all packages will produce a compilable result, for example due to unexported fields in an exported but not extracted struct type. Similarly, types may not agree; in the gzip example above, `yoink -pkg compress/gzip -y Writer,NewWriter` would produce uncompilable code as the copied `NewWriter` function would call `gzip.NewWriterLevel` and attempt to return the resulting `*gzip.Writer` which will not agree with the `NewWriter` signature.