package unused

import "go/types"

// lookupMethod returns the index of and method with matching package and name, or (-1, nil).
func lookupMethod(T *types.Interface, pkg *types.Package, name string) (int, *types.Func) {
	if name != "_" {
		for i := 0; i < T.NumMethods(); i++ {
			m := T.Method(i)
			if sameId(m, pkg, name) {
				return i, m
			}
		}
	}
	return -1, nil
}

func sameId(Obj types.Object, pkg *types.Package, name string) bool {
	// spec:
	// "Two identifiers are different if they are spelled differently,
	// or if they appear in different packages and are not exported.
	// Otherwise, they are the same."
	if name != Obj.Name() {
		return false
	}
	// obj.Name == name
	if Obj.Exported() {
		return true
	}
	// not exported, so packages must be the same (pkg == nil for
	// fields in Universe scope; this can only happen for types
	// introduced via Eval)
	if pkg == nil || Obj.Pkg() == nil {
		return pkg == Obj.Pkg()
	}
	// pkg != nil && obj.pkg != nil
	return pkg.Path() == Obj.Pkg().Path()
}

func implements(V types.Type, T *types.Interface, msV *types.MethodSet) ([]*types.Selection, bool) {
	// fast path for common case
	if T.Empty() {
		return nil, true
	}

	if ityp, _ := V.Underlying().(*types.Interface); ityp != nil {
		// TODO(dh): is this code reachable?
		for i := 0; i < T.NumMethods(); i++ {
			m := T.Method(i)
			_, Obj := lookupMethod(ityp, m.Pkg(), m.Name())
			switch {
			case Obj == nil:
				return nil, false
			case !types.Identical(Obj.Type(), m.Type()):
				return nil, false
			}
		}
		return nil, true
	}

	// A concrete type implements T if it implements all methods of T.
	var sels []*types.Selection
	for i := 0; i < T.NumMethods(); i++ {
		m := T.Method(i)
		sel := msV.Lookup(m.Pkg(), m.Name())
		if sel == nil {
			return nil, false
		}

		f, _ := sel.Obj().(*types.Func)
		if f == nil {
			return nil, false
		}

		if !types.Identical(f.Type(), m.Type()) {
			return nil, false
		}

		sels = append(sels, sel)
	}
	return sels, true
}
