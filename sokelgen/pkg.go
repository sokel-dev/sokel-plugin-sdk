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

// Package：解析好的一个 Go 包（多文件）。
//
// 单文件不够用——真实插件里 handler 与 Register 常常不在同一个文件，
// 入/出参 struct 也可能单独放一个 types.go。按包解析才是对的粒度。
type Package struct {
	Name  string
	scope *pkgScope
	funcs map[string]*ast.FuncType
	files []*ast.File
}

// LoadDir 解析目录下的所有 .go（跳过 _test.go 与生成物）。
func LoadDir(dir string) (*Package, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, fmt.Errorf("读取目录失败: %w", err)
	}
	var srcs []string
	for _, e := range entries {
		n := e.Name()
		if e.IsDir() || !strings.HasSuffix(n, ".go") || strings.HasSuffix(n, "_test.go") || n == generatedFileName {
			continue
		}
		b, err := os.ReadFile(filepath.Join(dir, n))
		if err != nil {
			return nil, fmt.Errorf("读取 %s: %w", n, err)
		}
		srcs = append(srcs, string(b))
	}
	if len(srcs) == 0 {
		return nil, fmt.Errorf("目录 %s 下没有可解析的 .go 文件", dir)
	}
	return Load(srcs...)
}

// Load 解析一组源码为一个包。
func Load(srcs ...string) (*Package, error) {
	p := &Package{
		scope: &pkgScope{structs: map[string]*ast.StructType{}, named: map[string]*ast.TypeSpec{}},
		funcs: map[string]*ast.FuncType{},
	}
	fset := token.NewFileSet()
	for i, src := range srcs {
		file, err := parser.ParseFile(fset, fmt.Sprintf("src%d.go", i), src, parser.ParseComments)
		if err != nil {
			return nil, fmt.Errorf("解析失败: %w", err)
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

// Fields 解析包内指定 struct 的契约字段。
func (p *Package) Fields(structName string) ([]Field, error) {
	st, ok := p.scope.structs[structName]
	if !ok {
		return nil, fmt.Errorf("找不到 struct %q", structName)
	}
	return p.scope.fieldsOf(st, map[string]bool{structName: true})
}

// Ops 扫描包内所有 sokel.Register 调用，并把每个操作的入/出参展开为完整契约。
func (p *Package) Ops() ([]OpIO, error) {
	ops, err := p.scanRegisters()
	if err != nil {
		return nil, err
	}
	for i := range ops {
		in, err := p.Fields(ops[i].InType)
		if err != nil {
			return nil, fmt.Errorf("操作 %q 的入参 %s: %w", ops[i].OpID, ops[i].InType, err)
		}
		out, err := p.Fields(ops[i].OutType)
		if err != nil {
			return nil, fmt.Errorf("操作 %q 的出参 %s: %w", ops[i].OpID, ops[i].OutType, err)
		}
		ops[i].Inputs, ops[i].Outputs = in, out
	}
	return ops, nil
}
