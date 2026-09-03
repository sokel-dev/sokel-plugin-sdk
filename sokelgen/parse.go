// Copyright 2026 The Sokel Authors
// SPDX-License-Identifier: Apache-2.0

// Package sokelgen derives plugin contracts from **source** (the AST) instead of runtime reflection.
//
// Why not reflection: the AST sees source-level information that reflection never can.
//   - oneOf has no other path: reflection cannot map a type name back to a type, and Go has no union
//     type;
//   - the comment above a field can serve as its desc, saving a great many repetitive tags;
//   - a duplicate field name, an unsupported type, or a oneof naming a type that does not exist is
//     reported at generation time rather than blowing up on the platform during the handshake.
//
// It uses only the standard go/parser and does not pull in golang.org/x/tools: an SDK user should not
// carry a heavy dependency for a generator. The cost is that cross-package type references cannot be
// resolved (a plugin's input structs are almost always in one package), and hitting one produces a
// clear error rather than a silent downgrade.
package sokelgen

import (
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"strconv"
	"strings"
)

// Field has the same shape as the runtime's Field. It is defined separately so the generator does not
// depend on the runtime package.
type Field struct {
	Name     string   `json:"name"`
	Label    string   `json:"label,omitempty"`
	Type     string   `json:"type"`
	Types    []string `json:"types,omitempty"`
	Required bool     `json:"required,omitempty"`
	Default  any      `json:"default,omitempty"`
	Desc     string   `json:"desc,omitempty"`
	// Placeholder 是输入框里的灰字提示（"sk-ant-..."）。平台的凭证表单一直支持它，
	// 而这里此前没有——于是把库里的存量凭证契约喂回 manifest 会被判「未知字段」。
	// 它对填凭证的人很有用：一个示例值省掉一次翻文档。
	Placeholder string `json:"placeholder,omitempty"`
	// Help 是字段下方的说明（"GCP 控制台 → IAM → 服务账号 → …"）。与 Placeholder 同类：
	// 平台的凭证表单一直支持，SDK 此前没有。凭证最难的往往不是填哪个框，而是**去哪儿拿**
	// 那个值——这行字省掉的是一次跨产品的翻文档。
	Help      string         `json:"help,omitempty"`
	Options   []Option       `json:"options,omitempty"`
	Fields    []Field        `json:"fields,omitempty"`
	ValueType *Field         `json:"valueType,omitempty"`
	GoType    string         `json:"goType,omitempty"`   // the declared Go type name: In/Out reuse it rather than rebuilding an equivalent
	ItemType  string         `json:"itemType,omitempty"` // an array's scalar element type ([]string versus []number)
	Opaque    bool           `json:"opaque,omitempty"`
	OneOf     []OneOfVariant `json:"oneOf,omitempty"`
}

// Option is one enum candidate. With an empty Label the frontend falls back to showing Value.
type Option struct {
	Value string `json:"value"`
	Label string `json:"label,omitempty"`
}

// OneOfVariant is one branch of a structural union. Name identifies the branch for error messages and
// the UI's selected state; the runtime value is the branch's own shape, with no discriminator wrapper.
type OneOfVariant struct {
	Name   string  `json:"name"`
	Label  string  `json:"label,omitempty"`
	Type   string  `json:"type"`
	GoType string  `json:"goType,omitempty"` // for an array branch, the element type ([]Block -> "Block")
	Fields []Field `json:"fields,omitempty"`
}

// ParseStructFields parses the contract fields of a named struct in a piece of source.
func ParseStructFields(src, structName string) ([]Field, error) {
	fset := token.NewFileSet()
	file, err := parser.ParseFile(fset, "src.go", src, parser.ParseComments)
	if err != nil {
		return nil, fmt.Errorf("parsing the source failed: %w", err)
	}
	p := &pkgScope{structs: map[string]*ast.StructType{}, named: map[string]*ast.TypeSpec{}}
	p.collect(file)
	st, ok := p.structs[structName]
	if !ok {
		return nil, fmt.Errorf("no struct named %q", structName)
	}
	return p.fieldsOf(st, map[string]bool{structName: true})
}

// pkgScope is the package's type table. Cross-package types are not in it, and descending into one
// produces a clear error rather than a silent downgrade. named holds every named type, not only
// structs, because a oneof may name something like `type BlocksArray []Block`.
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
				ts.Doc = gd.Doc // for a single-type declaration the comment hangs off the GenDecl
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
		// Anonymous embedded fields: reflection does not expand them (which once left a plugin reporting
		// only three inputs), so the AST skips them explicitly too and the two paths agree. Flatten them
		// by hand if you want them.
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
			// desc: an explicit tag wins; otherwise the comment above the field, which only the AST sees.
			if d := tagValue(tag, "desc"); d != "" {
				f.Desc = d
			} else {
				f.Desc = docText(fld.Doc)
			}
			typ, sub, vt, opaque, err := p.typeOf(fld.Type, seen)
			if err != nil {
				return nil, fmt.Errorf("field %s: %w", name, err)
			}
			f.Type, f.Fields, f.ValueType, f.Opaque = typ, sub, vt, opaque
			// An array's scalar element type goes into ItemType, never ValueType, which belongs to a map's
			// dynamic keys.
			if f.Type == "array" && f.ValueType != nil && len(f.ValueType.Fields) == 0 {
				f.ItemType, f.ValueType = f.ValueType.Type, nil
			}
			// Remember the named type: reverse migration generates field.Json("doc", DocStruct{}) to
			// reference a type the plugin already has, rather than rebuilding an equivalent one.
			f.GoType = p.namedTypeOf(fld.Type)
			// Integers: the reflection path recorded the width long ago and the AST path did not, so a
			// migrated `ContentID int` became a float64 and the implementation needed a conversion out of
			// nowhere. The two paths must derive the same thing. Arrays need it too ([]int has integer
			// elements, matching field.Ints: GoType=int plus ItemType=number).
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
			// `enum:"a,b"` gives bare values, `enum:"a=A,b=B"` adds display names — the same rule as the
			// runtime side.
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
			// oneof:"TypeA,TypeB" is a structural union. **Only the AST can do this**: reflection cannot
			// map a type name back to a type, and Go has no union type.
			if ov := tagValue(tag, "oneof"); ov != "" {
				variants, err := p.oneOfVariants(ov, seen)
				if err != nil {
					return nil, fmt.Errorf("field %s's oneof: %w", name, err)
				}
				f.OneOf = variants
				f.Opaque = false // declared branches are not "no structure", just several shapes
			}
			hasDefault := false
			if dv, ok := lookupTag(tag, "default"); ok {
				f.Default = coerceDefault(dv, f.Type)
				hasDefault = true
			}
			// Required follows the same rule as reflection: not optional, no default, not a pointer (files
			// excepted, see the runtime comment).
			_, isPtr := fld.Type.(*ast.StarExpr)
			f.Required = !optional && !hasDefault && (!isPtr || f.Type == "file")
			out = append(out, f)
		}
	}
	return out, nil
}

// typeOf maps an AST type expression to a contract type, returning (type, sub-fields, valueType, opaque).
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
			return "json", nil, nil, true, nil // a bare any has no structure to speak of
		}
		// A named type in this package: a struct expands recursively, anything else (aliases included)
		// resolves to its underlying type. `type tag = map[string]any` with `[]tag` looks like a slice of
		// structs but is really []map[string]any — without resolving the alias it would be treated as
		// structured, and the contract would not match reality.
		// A cycle stops the descent: a self-referential type should not blow up the generator.
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
			// A named non-struct (alias included) resolves to its underlying type; the caller records the
			// name if it needs it.
			return p.typeOf(ts.Type, seen)
		}
		return "string", nil, nil, false, nil
	case *ast.InterfaceType:
		return "json", nil, nil, true, nil
	case *ast.StructType:
		// An anonymous struct literal, and the underlying structure of a named type handed in by a oneof
		// branch.
		sub, err := p.fieldsOf(t, seen)
		if err != nil {
			return "", nil, nil, false, err
		}
		return "json", sub, nil, len(sub) == 0, nil
	case *ast.SelectorExpr:
		// File is the SDK's own type and the only marker of a file parameter; the reflection path special-
		// cases it too.
		if isSokelFile(t) {
			return "file", nil, nil, false, nil
		}
		// Any other cross-package type: the standard parser cannot see its definition. Rather than
		// silently treating it as a string, this errors out so the author moves the type into this package
		// or declares it explicitly — silent downgrades are exactly what this type system exists to remove.
		return "", nil, nil, false, fmt.Errorf("cannot resolve the structure of cross-package type %s; define it in this package or declare it explicitly with `type:\"...\"`", exprString(t))
	case *ast.ArrayType:
		// A file array becomes array<file>, with the element type passed back through the ValueType
		// channel for the caller to store in ItemType. Same as the reflection path: a file list has exactly
		// one spelling.
		if isFileExpr(t.Elt) {
			return "array", nil, &Field{Type: "file"}, false, nil
		}
		et, sub, _, elemOpaque, err := p.typeOf(t.Elt, seen)
		if err != nil {
			return "", nil, nil, false, err
		}
		// Structureless elements (a bare map, any) mean the array itself has no structural constraint, so
		// mark it opaque. Scalar elements ([]string) are **not** opaque: the element type is definite and
		// goes into ItemType. Missing this step made reverse migration treat []string as structureless and
		// emit field.Object — observed in practice.
		if !elemOpaque && len(sub) == 0 && isScalarType(et) {
			return "array", nil, &Field{Type: et}, false, nil // the element scalar type rides the ValueType channel
		}
		return "array", sub, nil, elemOpaque && len(sub) == 0, nil
	case *ast.MapType:
		if id, ok := t.Key.(*ast.Ident); !ok || id.Name != "string" {
			return "json", nil, nil, true, nil // a non-string key cannot be expressed as a JSON object
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

// parseSokelTag reads the external name and the optional flag, by the same rule as the runtime: no tag
// means the snake_case form of the field name.
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

// oneOfVariants locates each type named in `oneof:"A,B"` within the package and expands it into a
// branch. A missing type is an error rather than a skip: silently dropping a branch leaves the user to
// discover at runtime that "this shape is not accepted".
func (p *pkgScope) oneOfVariants(tagVal string, seen map[string]bool) ([]OneOfVariant, error) {
	var out []OneOfVariant
	for _, raw := range strings.Split(tagVal, ",") {
		tn := strings.TrimSpace(raw)
		if tn == "" {
			continue
		}
		ts, ok := p.named[tn]
		if !ok {
			return nil, fmt.Errorf("no type named %q in this package (alias a cross-package type here first)", tn)
		}
		typ, sub, _, _, err := p.typeOf(ts.Type, seen)
		if err != nil {
			return nil, fmt.Errorf("expanding %s: %w", tn, err)
		}
		out = append(out, OneOfVariant{
			Name:   tn,
			Label:  docText(ts.Doc), // the comment above the type becomes the branch label; only the AST sees it
			Type:   typ,
			Fields: sub,
		})
	}
	return out, nil
}

// isSokelFile reports whether an expression is sokel.File.
func isSokelFile(e ast.Expr) bool {
	sel, ok := e.(*ast.SelectorExpr)
	if !ok {
		return false
	}
	id, ok := sel.X.(*ast.Ident)
	return ok && id.Name == "sokel" && sel.Sel.Name == "File"
}

// isFileExpr covers sokel.File and *sokel.File.
func isFileExpr(e ast.Expr) bool {
	if star, ok := e.(*ast.StarExpr); ok {
		return isSokelFile(star.X)
	}
	return isSokelFile(e)
}

// namedTypeOf returns the type name when a field is a named struct in this package (or a slice or
// pointer to one). Anonymous structs and scalars return "": they have no name to reference.
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

// isScalarType reports a scalar contract type; such an array element fills ItemType instead of
// making the array opaque.
func isScalarType(t string) bool {
	switch t {
	case "string", "text", "number", "boolean", "enum":
		return true
	}
	return false
}

// astIntKind returns the type name when an AST expression is an integer type (through pointers and
// slice elements too), and "" otherwise.
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
