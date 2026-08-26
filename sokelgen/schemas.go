// Copyright 2026 The Sokel Authors
// SPDX-License-Identifier: Apache-2.0

package sokelgen

import (
	"fmt"
	"go/ast"
	"sort"
)

// schemaMethods is the method set that makes up contract.Schema (an operation).
var schemaMethods = []string{"Meta", "Inputs", "Outputs"}

// eventMethods is the method set that makes up contract.EventSchema (an event).
var eventMethods = []string{"EventMeta", "Fields"}

// commonMethods is the optional common-fields declaration.
var commonMethods = []string{"CommonFields"}

// SchemaTypes finds the names of types in the package that implement sokel.Schema, sorted by name so
// that generation is deterministic.
//
// The test is **the method set** rather than an explicit interface assertion: nobody writes
// `var _ sokel.Schema = FileDigest{}` of their own accord, and requiring it would add a ceremony for
// nothing. Value and pointer receivers both count — real plugins use both.
// EventTypes finds the names of types implementing contract.EventSchema, sorted by name so that
// generation is deterministic.
func (p *Package) EventTypes() []string { return p.typesWith(eventMethods) }

// CommonFieldsTypes finds the types that declare common fields (usually at most one).
func (p *Package) CommonFieldsTypes() []string { return p.typesWith(commonMethods) }

func (p *Package) SchemaTypes() []string { return p.typesWith(schemaMethods) }

func (p *Package) typesWith(want []string) []string {
	// type name -> the methods it implements
	got := map[string]map[string]bool{}
	for _, f := range p.files {
		for _, d := range f.Decls {
			fd, ok := d.(*ast.FuncDecl)
			if !ok || fd.Recv == nil || len(fd.Recv.List) == 0 {
				continue
			}
			recv := recvTypeName(fd.Recv.List[0].Type)
			if recv == "" {
				continue
			}
			if got[recv] == nil {
				got[recv] = map[string]bool{}
			}
			got[recv][fd.Name.Name] = true
		}
	}
	var out []string
	for name, methods := range got {
		complete := true
		for _, m := range want {
			if !methods[m] {
				complete = false
				break
			}
		}
		if complete {
			out = append(out, name)
		}
	}
	sort.Strings(out)
	return out
}

// SchemaOps lists every Schema type in the package so their declarations can be read afterwards.
// Finding none is an error: generating an empty file would be more confusing than generating nothing.
func (p *Package) SchemaOps() ([]string, error) {
	names := p.SchemaTypes()
	if len(names) == 0 {
		return nil, fmt.Errorf("no type in the package implements contract.Schema (it needs all three of Meta, Inputs and Outputs)")
	}
	return names, nil
}

// recvTypeName takes a method receiver's type name, normalising value, pointer and generic
// instantiation forms down to the bare name.
func recvTypeName(e ast.Expr) string {
	switch t := e.(type) {
	case *ast.StarExpr:
		return recvTypeName(t.X)
	case *ast.IndexExpr:
		return recvTypeName(t.X)
	case *ast.IndexListExpr:
		return recvTypeName(t.X)
	case *ast.Ident:
		return t.Name
	}
	return ""
}
