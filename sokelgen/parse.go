// Copyright 2026 The Sokel Authors
// SPDX-License-Identifier: Apache-2.0

// Package sokelgen 从**源码**（AST）推导插件契约，取代运行时反射。
//
// 为什么不是反射（docs/plugin-sdk-multilang.md §1）：AST 能看到源码级信息，反射永远看不到。
//   - oneOf 只有这条路：反射拿不到「类型名字符串 → 类型」的映射，Go 也没有联合类型
//   - 字段上方的注释可直接当 desc，省掉大量重复 tag
//   - 字段名冲突 / 不支持的类型 / oneof 引用了不存在的类型 → 生成期就报，
//     不必等插件启动、注册握手时才在平台侧炸
//
// 只用标准库 go/parser，不引入 golang.org/x/tools —— SDK 用户不该为一个生成器
// 背上重依赖。代价是跨包引用的类型解析不了（插件入参 struct 基本都在同一包），
// 遇到时报明确错误而不是静默降级。
package sokelgen

import (
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"strconv"
	"strings"
)

// Field 与 sokel.Field 同形（线协议 §5）。这里单独定义，避免生成器反向依赖运行时包。
type Field struct {
	Name      string         `json:"name"`
	Label     string         `json:"label,omitempty"`
	Type      string         `json:"type"`
	Types     []string       `json:"types,omitempty"`
	Required  bool           `json:"required,omitempty"`
	Default   any            `json:"default,omitempty"`
	Desc      string         `json:"desc,omitempty"`
	Options   []Option       `json:"options,omitempty"`
	Fields    []Field        `json:"fields,omitempty"`
	ValueType *Field         `json:"valueType,omitempty"`
	GoType    string         `json:"goType,omitempty"`   // 声明时的 Go 类型名：生成 In/Out 时复用它，不重造等价结构
	ItemType  string         `json:"itemType,omitempty"` // 数组元素的标量类型（[]string vs []number）
	Opaque    bool           `json:"opaque,omitempty"`
	OneOf     []OneOfVariant `json:"oneOf,omitempty"`
}

// Option：enum 的一个候选项（与 sokel.Option 同形）。Label 为空时前端回退显示 Value。
type Option struct {
	Value string `json:"value"`
	Label string `json:"label,omitempty"`
}

// OneOfVariant：结构联合的一个分支。Name 是分支标识（报错定位与 UI 选中态用），
// 运行值就是该分支本身的形状，不带 discriminator 包装。
type OneOfVariant struct {
	Name   string  `json:"name"`
	Label  string  `json:"label,omitempty"`
	Type   string  `json:"type"`
	GoType string  `json:"goType,omitempty"` // 数组分支指元素类型（[]Block → "Block"）
	Fields []Field `json:"fields,omitempty"`
}

// ParseStructFields 解析一段源码里指定 struct 的契约字段。
func ParseStructFields(src, structName string) ([]Field, error) {
	fset := token.NewFileSet()
	file, err := parser.ParseFile(fset, "src.go", src, parser.ParseComments)
	if err != nil {
		return nil, fmt.Errorf("解析源码失败: %w", err)
	}
	p := &pkgScope{structs: map[string]*ast.StructType{}, named: map[string]*ast.TypeSpec{}}
	p.collect(file)
	st, ok := p.structs[structName]
	if !ok {
		return nil, fmt.Errorf("找不到 struct %q", structName)
	}
	return p.fieldsOf(st, map[string]bool{structName: true})
}

// pkgScope：包内的类型表。跨包类型不在其中，下钻时报明确错误而非静默降级。
// named 收全部具名类型（不止 struct）——oneof 可以点名 `type BlocksArray []Block` 这种。
type pkgScope struct {
	structs map[string]*ast.StructType
	named   map[string]*ast.TypeSpec
}

func (p *pkgScope) collect(file *ast.File) {
	for _, decl := range file.Decls {
		gd, ok := decl.(*ast.GenDecl)
		if !ok || gd.Tok != token.TYPE {
			continue
		}
		for _, spec := range gd.Specs {
			ts, ok := spec.(*ast.TypeSpec)
			if !ok {
				continue
			}
			p.named[ts.Name.Name] = ts
			if ts.Doc == nil {
				ts.Doc = gd.Doc // 单类型声明的注释挂在 GenDecl 上
			}
			if st, ok := ts.Type.(*ast.StructType); ok {
				p.structs[ts.Name.Name] = st
			}
		}
	}
}

func (p *pkgScope) fieldsOf(st *ast.StructType, seen map[string]bool) ([]Field, error) {
	var out []Field
	for _, fld := range st.Fields.List {
		// 匿名嵌入字段：反射不展开它（曾让 tingwu 只报 3 个入参），AST 里同样显式跳过，
		// 保持与运行时一致；要用就显式平铺。
		if len(fld.Names) == 0 {
			continue
		}
		for _, nameIdent := range fld.Names {
			if !nameIdent.IsExported() {
				continue
			}
			tag := structTag(fld)
			name, optional := parseSokelTag(tag, nameIdent.Name)
			if name == "-" {
				continue
			}
			f := Field{Name: name, Label: tagValue(tag, "label")}
			// desc：显式 tag 优先；否则取字段上方的注释（AST 独有）。
			if d := tagValue(tag, "desc"); d != "" {
				f.Desc = d
			} else {
				f.Desc = docText(fld.Doc)
			}
			typ, sub, vt, opaque, err := p.typeOf(fld.Type, seen)
			if err != nil {
				return nil, fmt.Errorf("字段 %s: %w", name, err)
			}
			f.Type, f.Fields, f.ValueType, f.Opaque = typ, sub, vt, opaque
			// array 的标量元素类型走 ItemType，不占用 ValueType（那是 map 的动态键值类型）。
			if f.Type == "array" && f.ValueType != nil && len(f.ValueType.Fields) == 0 {
				f.ItemType, f.ValueType = f.ValueType.Type, nil
			}
			// 记住具名类型：反向迁移要生成 field.Json("doc", DocStruct{}) 引用插件里
			// 已存在的类型，而不是重造一个等价结构。
			f.GoType = p.namedTypeOf(fld.Type)
			// 整数：反射侧早就记了宽度，AST 侧漏了 —— 于是从旧代码迁移过来的 `ContentID int`
			// 变成 float64，实现里得凭空多一道转换。两条路径的推导必须一致。
			// array 也要（[]int 的元素是整数，与 field.Ints 同形：GoType=int + ItemType=number）。
			if (f.Type == "number" || f.Type == "array") && f.GoType == "" {
				f.GoType = astIntKind(fld.Type)
			}
			if tv := tagValue(tag, "type"); tv != "" {
				var ts []string
				for _, part := range strings.Split(tv, ",") {
					if part = strings.TrimSpace(part); part != "" {
						ts = append(ts, part)
					}
				}
				if len(ts) > 0 {
					f.Type = ts[0]
					if len(ts) > 1 {
						f.Types = ts
					}
				}
			}
			// `enum:"a,b"` → 纯值；`enum:"a=甲,b=乙"` → 带显示名（与 sokel 侧同规则）。
			if ev := tagValue(tag, "enum"); ev != "" {
				f.Type = "enum"
				for _, o := range strings.Split(ev, ",") {
					if o = strings.TrimSpace(o); o == "" {
						continue
					}
					val, label, _ := strings.Cut(o, "=")
					f.Options = append(f.Options, Option{Value: strings.TrimSpace(val), Label: strings.TrimSpace(label)})
				}
			}
			// oneof:"TypeA,TypeB" —— 结构联合。**只有 AST 能做**：反射拿不到
			// 「类型名字符串 → 类型」的映射，Go 也没有联合类型。
			if ov := tagValue(tag, "oneof"); ov != "" {
				variants, err := p.oneOfVariants(ov, seen)
				if err != nil {
					return nil, fmt.Errorf("字段 %s 的 oneof: %w", name, err)
				}
				f.OneOf = variants
				f.Opaque = false // 有分支声明就不是「无结构」，只是有多种形状
			}
			hasDefault := false
			if dv, ok := lookupTag(tag, "default"); ok {
				f.Default = coerceDefault(dv, f.Type)
				hasDefault = true
			}
			// 必填判定与反射同规则：非 optional、无 default、非指针（file 除外，见 sokel 注释）。
			_, isPtr := fld.Type.(*ast.StarExpr)
			f.Required = !optional && !hasDefault && (!isPtr || f.Type == "file")
			out = append(out, f)
		}
	}
	return out, nil
}

// typeOf 把 AST 类型表达式映射为契约类型；返回 (类型, 子字段, valueType, opaque)。
func (p *pkgScope) typeOf(expr ast.Expr, seen map[string]bool) (string, []Field, *Field, bool, error) {
	switch t := expr.(type) {
	case *ast.StarExpr:
		return p.typeOf(t.X, seen)
	case *ast.Ident:
		switch t.Name {
		case "bool":
			return "boolean", nil, nil, false, nil
		case "int", "int8", "int16", "int32", "int64",
			"uint", "uint8", "uint16", "uint32", "uint64", "float32", "float64":
			return "number", nil, nil, false, nil
		case "string":
			return "string", nil, nil, false, nil
		case "any":
			return "json", nil, nil, true, nil // 裸 any = 无结构可言
		}
		// 包内具名类型：struct 递归展开；其余（含类型别名）解析其底层类型。
		// `type tag = map[string]any` + `[]tag` 看着像结构体切片，实为 []map[string]any——
		// 不解别名就会把它当成有结构，契约与实际对不上。
		// 环点停止下钻（自引用类型不该把生成器打爆）。
		if ts, ok := p.named[t.Name]; ok {
			if seen[t.Name] {
				return "json", nil, nil, false, nil
			}
			seen[t.Name] = true
			defer delete(seen, t.Name)
			if st, isStruct := ts.Type.(*ast.StructType); isStruct {
				sub, err := p.fieldsOf(st, seen)
				if err != nil {
					return "", nil, nil, false, err
				}
				return "json", sub, nil, len(sub) == 0, nil
			}
			// 具名非 struct（含类型别名）解析其底层；名字由调用方按需记录。
			return p.typeOf(ts.Type, seen)
		}
		return "string", nil, nil, false, nil
	case *ast.InterfaceType:
		return "json", nil, nil, true, nil
	case *ast.StructType:
		// 匿名 struct 字面量，以及 oneof 分支直接传进来的具名类型底层结构。
		sub, err := p.fieldsOf(t, seen)
		if err != nil {
			return "", nil, nil, false, err
		}
		return "json", sub, nil, len(sub) == 0, nil
	case *ast.SelectorExpr:
		// sokel.File 是 SDK 自己的类型，也是文件参数的唯一标志（反射版本同样特判）。
		if isSokelFile(t) {
			return "file", nil, nil, false, nil
		}
		// 其余跨包类型：标准库 parser 拿不到其定义。不静默当 string，明确报错让作者改成
		// 本包类型或显式 type tag —— 静默降级正是这套类型系统要消灭的东西。
		return "", nil, nil, false, fmt.Errorf("跨包类型 %s 无法解析结构，请在本包定义或用 `type:\"...\"` 显式声明", exprString(t))
	case *ast.ArrayType:
		// 文件数组 → array<file>（元素类型借 ValueType 通道回传，caller 落 ItemType），
		// 与反射版本一致——文件列表的唯一表达（web docs/type-system.md §12）。
		if isFileExpr(t.Elt) {
			return "array", nil, &Field{Type: "file"}, false, nil
		}
		et, sub, _, elemOpaque, err := p.typeOf(t.Elt, seen)
		if err != nil {
			return "", nil, nil, false, err
		}
		// 元素无结构（裸 map / any）→ 整个数组也没有结构约束，据实标 opaque。
		// 标量元素（[]string）**不是** opaque：元素类型明确，落 ItemType。
		// 漏了这一步的话，反向迁移会把 []string 当成无结构，生成 field.Object —— 实测踩到。
		if !elemOpaque && len(sub) == 0 && isScalarType(et) {
			return "array", nil, &Field{Type: et}, false, nil // 借 ValueType 回传元素标量类型
		}
		return "array", sub, nil, elemOpaque && len(sub) == 0, nil
	case *ast.MapType:
		if id, ok := t.Key.(*ast.Ident); !ok || id.Name != "string" {
			return "json", nil, nil, true, nil // 非 string 键无法用 JSON 对象表达
		}
		vt, sub, vvt, vopaque, err := p.typeOf(t.Value, seen)
		if err != nil {
			return "", nil, nil, false, err
		}
		if vopaque && len(sub) == 0 {
			return "json", nil, nil, true, nil // map[string]any
		}
		return "json", nil, &Field{Type: vt, Fields: sub, ValueType: vvt}, false, nil
	}
	return "string", nil, nil, false, nil
}

func structTag(f *ast.Field) string {
	if f.Tag == nil {
		return ""
	}
	s, err := strconv.Unquote(f.Tag.Value)
	if err != nil {
		return ""
	}
	return s
}

func lookupTag(tag, key string) (string, bool) {
	return reflectStructTag(tag).Lookup(key)
}

func tagValue(tag, key string) string {
	v, _ := lookupTag(tag, key)
	return v
}

// parseSokelTag 取对外名与 optional 标记；与 sokel.parseSokelTag 同规则（无 tag 时用下划线小写）。
func parseSokelTag(tag, fieldName string) (string, bool) {
	raw, ok := lookupTag(tag, "sokel")
	if !ok || raw == "" {
		return toSnake(fieldName), false
	}
	parts := strings.Split(raw, ",")
	name := strings.TrimSpace(parts[0])
	if name == "" {
		name = toSnake(fieldName)
	}
	optional := false
	for _, p := range parts[1:] {
		if strings.TrimSpace(p) == "optional" {
			optional = true
		}
	}
	return name, optional
}

func docText(g *ast.CommentGroup) string {
	if g == nil {
		return ""
	}
	return strings.TrimSpace(g.Text())
}

func exprString(e ast.Expr) string {
	if s, ok := e.(*ast.SelectorExpr); ok {
		if x, ok := s.X.(*ast.Ident); ok {
			return x.Name + "." + s.Sel.Name
		}
	}
	return "?"
}

// oneOfVariants 把 `oneof:"A,B"` 里点名的类型逐个在包内定位并展开为分支。
// 找不到就报错而不是跳过——静默丢掉一个分支，用户只会在运行时发现"这个形状不被接受"。
func (p *pkgScope) oneOfVariants(tagVal string, seen map[string]bool) ([]OneOfVariant, error) {
	var out []OneOfVariant
	for _, raw := range strings.Split(tagVal, ",") {
		tn := strings.TrimSpace(raw)
		if tn == "" {
			continue
		}
		ts, ok := p.named[tn]
		if !ok {
			return nil, fmt.Errorf("类型 %q 在本包内找不到（跨包类型请在本包定义别名）", tn)
		}
		typ, sub, _, _, err := p.typeOf(ts.Type, seen)
		if err != nil {
			return nil, fmt.Errorf("展开 %s: %w", tn, err)
		}
		out = append(out, OneOfVariant{
			Name:   tn,
			Label:  docText(ts.Doc), // 类型上方的注释即分支标签（AST 独有）
			Type:   typ,
			Fields: sub,
		})
	}
	return out, nil
}

// isSokelFile 判断表达式是否为 sokel.File。
func isSokelFile(e ast.Expr) bool {
	sel, ok := e.(*ast.SelectorExpr)
	if !ok {
		return false
	}
	id, ok := sel.X.(*ast.Ident)
	return ok && id.Name == "sokel" && sel.Sel.Name == "File"
}

// isFileExpr：sokel.File 或 *sokel.File。
func isFileExpr(e ast.Expr) bool {
	if star, ok := e.(*ast.StarExpr); ok {
		return isSokelFile(star.X)
	}
	return isSokelFile(e)
}

// namedTypeOf：字段类型若是包内具名 struct（或其切片/指针），返回类型名。
// 匿名结构与标量返回空——它们没有可引用的名字。
func (p *pkgScope) namedTypeOf(e ast.Expr) string {
	switch t := e.(type) {
	case *ast.StarExpr:
		return p.namedTypeOf(t.X)
	case *ast.ArrayType:
		return p.namedTypeOf(t.Elt)
	case *ast.Ident:
		if _, ok := p.structs[t.Name]; ok {
			return t.Name
		}
	}
	return ""
}

// isScalarType：标量契约类型（数组元素是它时落 ItemType 而非判 opaque）。
func isScalarType(t string) bool {
	switch t {
	case "string", "text", "number", "boolean", "enum":
		return true
	}
	return false
}

// astIntKind：AST 表达式若是整数类型（含指针/切片元素），返回其类型名，否则空。
func astIntKind(e ast.Expr) string {
	switch t := e.(type) {
	case *ast.StarExpr:
		return astIntKind(t.X)
	case *ast.ArrayType:
		return astIntKind(t.Elt)
	case *ast.Ident:
		switch t.Name {
		case "int", "int8", "int16", "int32", "int64",
			"uint", "uint8", "uint16", "uint32", "uint64":
			return t.Name
		}
	}
	return ""
}
