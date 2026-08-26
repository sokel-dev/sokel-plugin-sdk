// Copyright 2026 The Sokel Authors
// SPDX-License-Identifier: Apache-2.0

package sokelgen

import (
	"fmt"
	"go/ast"
	"go/token"
	"strconv"
)

// ScanOps scans a single source file (a thin wrapper; real use goes through LoadDir -> Ops, see
// pkg.go).
func ScanOps(src string) ([]OpIO, error) {
	pkg, err := Load(src)
	if err != nil {
		return nil, err
	}
	return pkg.scanRegisters()
}

// scanRegisters walks every sokel.Register call in the package and extracts the mapping from
// operation id to the In/Out type names.
//
// In real plugins the type arguments are **inferred** — the code says sokel.Register(p, op, handler)
// with no explicit [In, Out]. So the types have to be read back out of the handler's signature:
// func(sokel.Ctx, In, *sokel.Emitter[Out]) error. A handler lives in one of three places, all of which
// must be recognised: an inline closure, a named function in the same file, and **a named function in
// another file** — that last one being the whole reason parsing happens per package.
func (p *Package) scanRegisters() ([]OpIO, error) {
	funcs := p.funcs
	var out []OpIO
	var scanErr error
	visit := func(n ast.Node) bool {
		call, ok := n.(*ast.CallExpr)
		if !ok || !isSelector(call.Fun, "sokel", "Register") || len(call.Args) < 3 {
			return true
		}
		opID, err := operationID(call.Args[1])
		if err != nil {
			scanErr = err
			return false
		}
		ft, err := handlerType(call.Args[2], funcs)
		if err != nil {
			scanErr = fmt.Errorf("operation %q: %w", opID, err)
			return false
		}
		in, outT, err := inOutTypes(ft)
		if err != nil {
			scanErr = fmt.Errorf("operation %q: %w", opID, err)
			return false
		}
		out = append(out, OpIO{OpID: opID, InType: in, OutType: outT})
		return false
	}
	for _, f := range p.files {
		ast.Inspect(f, visit)
	}
	if scanErr != nil {
		return nil, scanErr
	}
	return out, nil
}

// isSelector reports whether an expression is pkg.Name (also accepting the form wrapped in a generic
// instantiation).
func isSelector(e ast.Expr, pkg, name string) bool {
	switch t := e.(type) {
	case *ast.IndexExpr: // one explicit type argument
		return isSelector(t.X, pkg, name)
	case *ast.IndexListExpr: // several explicit type arguments
		return isSelector(t.X, pkg, name)
	case *ast.SelectorExpr:
		id, ok := t.X.(*ast.Ident)
		return ok && id.Name == pkg && t.Sel.Name == name
	}
	return false
}

// operationID reads the literal id out of sokel.Operation{ID: "..."}.
//
// A non-literal (one built from variables) is an outright error: skipping it silently would mean that
// operation's contract never appears, and the author would find out only after starting the plugin and
// failing to see the operation on the platform.
func operationID(arg ast.Expr) (string, error) {
	lit, ok := arg.(*ast.CompositeLit)
	if !ok {
		return "", fmt.Errorf("Register's second argument must be a sokel.Operation{...} literal")
	}
	for _, el := range lit.Elts {
		kv, ok := el.(*ast.KeyValueExpr)
		if !ok {
			continue
		}
		key, ok := kv.Key.(*ast.Ident)
		if !ok || key.Name != "ID" {
			continue
		}
		bl, ok := kv.Value.(*ast.BasicLit)
		if !ok || bl.Kind != token.STRING {
			return "", fmt.Errorf("Operation.ID must be a string literal (this one is a variable or expression, which the generator cannot evaluate statically)")
		}
		s, err := strconv.Unquote(bl.Value)
		if err != nil {
			return "", fmt.Errorf("failed to parse Operation.ID: %w", err)
		}
		return s, nil
	}
	return "", fmt.Errorf("Operation has no ID field")
}

func handlerType(arg ast.Expr, funcs map[string]*ast.FuncType) (*ast.FuncType, error) {
	switch t := arg.(type) {
	case *ast.FuncLit: // an inline closure
		return t.Type, nil
	case *ast.Ident: // a named function
		if ft, ok := funcs[t.Name]; ok {
			return ft, nil
		}
		return nil, fmt.Errorf("handler function %q not found (handlers in another package cannot be resolved; keep it in this one)", t.Name)
	}
	return nil, fmt.Errorf("unrecognised handler form (expected a function name or an inline closure)")
}

// inOutTypes extracts the In and Out type names from func(sokel.Ctx, In, *sokel.Emitter[Out]) error.
func inOutTypes(ft *ast.FuncType) (string, string, error) {
	params := flattenParams(ft)
	if len(params) != 3 {
		return "", "", fmt.Errorf("a handler signature must be func(sokel.Ctx, In, *sokel.Emitter[Out]) error, but this one has %d parameters", len(params))
	}
	in := typeName(params[1])
	if in == "" {
		return "", "", fmt.Errorf("the input type must be a named type in this package (this one is unrecognised)")
	}
	out := emitterTypeArg(params[2])
	if out == "" {
		return "", "", fmt.Errorf("the output parameter must be *sokel.Emitter[T] with T a named type in this package")
	}
	return in, out, nil
}

// flattenParams expands shared-type parameter lists such as func(a, b In).
func flattenParams(ft *ast.FuncType) []ast.Expr {
	var out []ast.Expr
	if ft.Params == nil {
		return out
	}
	for _, f := range ft.Params.List {
		n := len(f.Names)
		if n == 0 {
			n = 1 // an unnamed parameter
		}
		for i := 0; i < n; i++ {
			out = append(out, f.Type)
		}
	}
	return out
}

func typeName(e ast.Expr) string {
	if id, ok := e.(*ast.Ident); ok {
		return id.Name
	}
	return ""
}

// emitterTypeArg pulls the T out of *sokel.Emitter[T].
func emitterTypeArg(e ast.Expr) string {
	star, ok := e.(*ast.StarExpr)
	if !ok {
		return ""
	}
	idx, ok := star.X.(*ast.IndexExpr)
	if !ok {
		return ""
	}
	if !isSelector(idx.X, "sokel", "Emitter") {
		return ""
	}
	return typeName(idx.Index)
}
