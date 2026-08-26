// Copyright 2026 The Sokel Authors
// SPDX-License-Identifier: Apache-2.0

package sokelgen

import (
	"fmt"
	"go/ast"
	"go/token"
	"strconv"
)

// ScanOps 扫描单个源码（薄封装；真实使用走 LoadDir → Ops，见 pkg.go）。
func ScanOps(src string) ([]OpIO, error) {
	pkg, err := Load(src)
	if err != nil {
		return nil, err
	}
	return pkg.scanRegisters()
}

// scanRegisters 扫包内所有 sokel.Register 调用，取出「操作 id ↔ 入/出参类型名」。
//
// 真实插件里类型参数是**推断**的——写的是 sokel.Register(p, op, handler)，没有显式
// [In, Out]。所以类型只能从 handler 签名反推：func(sokel.Ctx, In, *sokel.Emitter[Out]) error。
// handler 有三种落点，都要认：内联闭包（sysinfo）、同文件具名函数（report-pipeline）、
// **另一个文件里的具名函数**——最后这种正是按包解析的理由。
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
			scanErr = fmt.Errorf("操作 %q: %w", opID, err)
			return false
		}
		in, outT, err := inOutTypes(ft)
		if err != nil {
			scanErr = fmt.Errorf("操作 %q: %w", opID, err)
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

// isSelector 判断表达式是否为 pkg.Name（也接受被泛型实例化包裹的形态）。
func isSelector(e ast.Expr, pkg, name string) bool {
	switch t := e.(type) {
	case *ast.IndexExpr: // 显式单类型参数
		return isSelector(t.X, pkg, name)
	case *ast.IndexListExpr: // 显式多类型参数
		return isSelector(t.X, pkg, name)
	case *ast.SelectorExpr:
		id, ok := t.X.(*ast.Ident)
		return ok && id.Name == pkg && t.Sel.Name == name
	}
	return false
}

// operationID 从 sokel.Operation{ID: "..."} 里取字面量 id。
// 非字面量（变量拼的）直接报错：静默跳过的后果是那个操作的契约永远不出现，
// 而作者要等插件启动、平台上看不到这个操作才发现。
func operationID(arg ast.Expr) (string, error) {
	lit, ok := arg.(*ast.CompositeLit)
	if !ok {
		return "", fmt.Errorf("Register 的第二参数应是 sokel.Operation{...} 字面量")
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
			return "", fmt.Errorf("Operation.ID 必须是字符串字面量（当前是变量/表达式，生成器无法静态取值）")
		}
		s, err := strconv.Unquote(bl.Value)
		if err != nil {
			return "", fmt.Errorf("Operation.ID 解析失败: %w", err)
		}
		return s, nil
	}
	return "", fmt.Errorf("Operation 缺少 ID 字段")
}

func handlerType(arg ast.Expr, funcs map[string]*ast.FuncType) (*ast.FuncType, error) {
	switch t := arg.(type) {
	case *ast.FuncLit: // 内联闭包
		return t.Type, nil
	case *ast.Ident: // 具名函数
		if ft, ok := funcs[t.Name]; ok {
			return ft, nil
		}
		return nil, fmt.Errorf("找不到 handler 函数 %q（跨包的 handler 无法解析，请放在本包内）", t.Name)
	}
	return nil, fmt.Errorf("无法识别的 handler 形态（应为函数名或内联闭包）")
}

// inOutTypes 从 func(sokel.Ctx, In, *sokel.Emitter[Out]) error 提取 In / Out 的类型名。
func inOutTypes(ft *ast.FuncType) (string, string, error) {
	params := flattenParams(ft)
	if len(params) != 3 {
		return "", "", fmt.Errorf("handler 签名应为 func(sokel.Ctx, In, *sokel.Emitter[Out]) error，实际有 %d 个参数", len(params))
	}
	in := typeName(params[1])
	if in == "" {
		return "", "", fmt.Errorf("入参类型必须是本包的具名类型（当前无法识别）")
	}
	out := emitterTypeArg(params[2])
	if out == "" {
		return "", "", fmt.Errorf("出参应为 *sokel.Emitter[T] 且 T 是本包具名类型")
	}
	return in, out, nil
}

// flattenParams 展开 `func(a, b In)` 这种共享类型的参数写法。
func flattenParams(ft *ast.FuncType) []ast.Expr {
	var out []ast.Expr
	if ft.Params == nil {
		return out
	}
	for _, f := range ft.Params.List {
		n := len(f.Names)
		if n == 0 {
			n = 1 // 匿名参数
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

// emitterTypeArg 取 *sokel.Emitter[T] 里的 T。
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
