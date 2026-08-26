// Copyright 2026 The Sokel Authors
// SPDX-License-Identifier: Apache-2.0

package sokelgen

import (
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"strings"
)

// Package is one parsed Go package, across all its files.
//
// A single file is not enough: in real plugins the handler and its Register call often live in
// different files, and the In/Out structs may sit in a types.go of their own. The package is the right
// granularity.
type Package struct {
	Name  string
	scope *pkgScope
	funcs map[string]*ast.FuncType
	files []*ast.File
}

// LoadDir parses every .go file in a directory, skipping _test.go files and generated output.
func LoadDir(dir string) (*Package, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, fmt.Errorf("failed to read the directory: %w", err)
	}
	var srcs []string
	for _, e := range entries {
		n := e.Name()
		if e.IsDir() || !strings.HasSuffix(n, ".go") || strings.HasSuffix(n, "_test.go") || n == generatedFileName {
			continue
		}
		b, err := os.ReadFile(filepath.Join(dir, n))
		if err != nil {
			return nil, fmt.Errorf("failed to read %s: %w", n, err)
		}
		srcs = append(srcs, string(b))
	}
	if len(srcs) == 0 {
		return nil, fmt.Errorf("no parsable .go files under %s", dir)
	}
	return Load(srcs...)
}

// Load parses a set of sources into one package.
func Load(srcs ...string) (*Package, error) {
	p := &Package{
		scope: &pkgScope{structs: map[string]*ast.StructType{}, named: map[string]*ast.TypeSpec{}},
		funcs: map[string]*ast.FuncType{},
	}
	fset := token.NewFileSet()
	for i, src := range srcs {
		file, err := parser.ParseFile(fset, fmt.Sprintf("src%d.go", i), src, parser.ParseComments)
		if err != nil {
			return nil, fmt.Errorf("parse failed: %w", err)
		}
		if p.Name == "" {
			p.Name = file.Name.Name
		}
		p.scope.collect(file)
		for _, d := range file.Decls {
			if fd, ok := d.(*ast.FuncDecl); ok && fd.Recv == nil {
				p.funcs[fd.Name.Name] = fd.Type
			}
		}
		p.files = append(p.files, file)
	}
	return p, nil
}

// Fields resolves the contract fields of a named struct in the package.
func (p *Package) Fields(structName string) ([]Field, error) {
	st, ok := p.scope.structs[structName]
	if !ok {
		return nil, fmt.Errorf("struct %q not found", structName)
	}
	return p.scope.fieldsOf(st, map[string]bool{structName: true})
}

// Ops scans every sokel.Register call in the package and expands each operation's inputs and outputs
// into a full contract.
func (p *Package) Ops() ([]OpIO, error) {
	ops, err := p.scanRegisters()
	if err != nil {
		return nil, err
	}
	for i := range ops {
		in, err := p.Fields(ops[i].InType)
		if err != nil {
			return nil, fmt.Errorf("operation %q input %s: %w", ops[i].OpID, ops[i].InType, err)
		}
		out, err := p.Fields(ops[i].OutType)
		if err != nil {
			return nil, fmt.Errorf("operation %q output %s: %w", ops[i].OpID, ops[i].OutType, err)
		}
		ops[i].Inputs, ops[i].Outputs = in, out
	}
	return ops, nil
}
