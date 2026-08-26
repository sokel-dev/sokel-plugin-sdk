// Copyright 2026 The Sokel Authors
// SPDX-License-Identifier: Apache-2.0

package sokelgen

import (
	"fmt"
	"go/ast"
	"sort"
)

// schemaMethods：构成 contract.Schema（操作）的方法集。
var schemaMethods = []string{"Meta", "Inputs", "Outputs"}

// eventMethods：构成 contract.EventSchema（事件）的方法集。
var eventMethods = []string{"EventMeta", "Fields"}

// commonMethods：可选的「公共字段」声明。
var commonMethods = []string{"CommonFields"}

// SchemaTypes 找出包内实现了 sokel.Schema 的类型名（按名字排序，保证生成确定性）。
//
// 按**方法集**判定而非显式接口断言：作者不会主动写
// `var _ sokel.Schema = FileDigest{}`，强制他写等于凭空加一道仪式。
// 值接收者与指针接收者都算——两种写法在真实插件里都有。
// EventTypes 找出包内实现了 contract.EventSchema 的类型名（按名字排序，保证生成确定性）。
func (p *Package) EventTypes() []string { return p.typesWith(eventMethods) }

// CommonFieldsTypes 找出声明了公共字段的类型（通常至多一个）。
func (p *Package) CommonFieldsTypes() []string { return p.typesWith(commonMethods) }

func (p *Package) SchemaTypes() []string { return p.typesWith(schemaMethods) }

func (p *Package) typesWith(want []string) []string {
	// 类型名 → 已实现的方法集
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

// SchemaOps 列出包内所有 Schema 类型，供后续取声明。
// 找不到就报错——生成一个空文件比不生成更让人困惑。
func (p *Package) SchemaOps() ([]string, error) {
	names := p.SchemaTypes()
	if len(names) == 0 {
		return nil, fmt.Errorf("包内没有实现 contract.Schema 的类型（需要 Meta / Inputs / Outputs 三个方法）")
	}
	return names, nil
}

// recvTypeName 取方法接收者的类型名（值 / 指针 / 泛型实例化都归一到裸名字）。
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
