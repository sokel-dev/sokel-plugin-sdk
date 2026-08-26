// Copyright 2026 The Sokel Authors
// SPDX-License-Identifier: Apache-2.0

// Package field provides the builders that declare a plugin contract.
//
// Why not struct tags:
//   - a tag is a string, so a misspelled `lable:"..."` leaves **the compiler silent**, while a
//     misspelled builder method fails to compile;
//   - structured information was already straining against strings — an enum's display name needed an
//     invented `=` separator, and a oneof's type name could only be spelled out;
//   - `oneof` here is a **real type reference**, so renaming or deleting the type fails to compile
//     immediately rather than at generation time;
//   - a structureless json field must say .Opaque(reason) to exist at all: the API simply has no
//     "just use a map" shortcut.
//
// A declaration is plain data and serialises straight to JSON for other languages' SDKs to generate
// types from. It lives with the contract rather than in the SDK because the builders use contract
// types and nothing to do with transport — putting them in the SDK would force any in-process kernel
// that wants to declare a contract to depend on the SDK in turn.
package field

import (
	"reflect"

	"github.com/sokel-dev/sokel-plugin-sdk/contract"
)

// B is the field builder. It implements contract.FieldSpec, so it goes straight into what
// Inputs()/Outputs() return.
type B struct{ f contract.Field }

// Field hands over the constructed contract field, implementing contract.FieldSpec.
func (b *B) Field() contract.Field { return b.f }

func mk(name string, t contract.ParamType) *B {
	// Required by default, matching the older tag semantics (not optional and no default means
	// required). That makes migration safer: forgetting .Required() cannot quietly turn a required
	// field into an optional one.
	return &B{f: contract.Field{Name: name, Type: t, Required: true}}
}

// —— Constructors take what they need as **required parameters**, not optional chained calls ——
//
// The trouble with chaining is that it can be skipped: `field.Json("os")` compiles with nothing
// attached and quietly produces a structureless json field. Put the type in a parameter and the
// compiler does the checking for you.

func String(name string) *B { return mk(name, contract.TString) }
func Text(name string) *B   { return mk(name, "text") }
func Number(name string) *B { return mk(name, contract.TNumber) }

// Int is an integer. **The contract type is still number**: the wire protocol has no int and the
// platform would not know one. GoType carries an "actually an integer" hint so the Go and Python
// sides generate int rather than float64. The distinction exists only on the plugin side — changing
// the wire protocol for it is not worth it, and casting float64(...) around every numeric field in
// the implementation reads terribly.
func Int(name string) *B  { b := mk(name, contract.TNumber); b.f.GoType = "int"; return b }
func Bool(name string) *B { return mk(name, contract.TBool) }
func File(name string) *B { return mk(name, contract.TFile) }

// Files is a file list: array<file>, the only spelling a file list has.
func Files(name string) *B {
	b := mk(name, contract.TArray)
	b.f.ItemType = contract.TFile
	return b
}

// Secret is a masked field, **for credentials**: the form hides it and the platform encrypts it.
//
// What separates it from String is not the Go type (both are string) but how the platform treats it,
// which is why it must be its own constructor rather than something the caller remembers to chain on.
func Secret(name string) *B { return mk(name, "secret") }

// Select is a dropdown, **for credentials**, with the candidates as a required parameter.
//
// Its type name is "select" rather than Enum's "enum", because that is what the credential form
// recognises. Two names for nearly one thing is awkward, but changing either would touch existing
// contracts, and here it only takes picking the right one.
func Select(name string, options ...string) *B {
	b := mk(name, "select")
	for _, o := range options {
		b.f.Options = append(b.f.Options, contract.Option{Value: o, Label: o})
	}
	return b
}

// Enum takes its candidates as a required parameter: an empty enum means nothing.
func Enum(name string, opts ...contract.Option) *B {
	b := mk(name, contract.TEnum)
	b.f.Options = opts
	return b
}

// Json is an object whose **structure comes from a Go type**:
//
//	field.Json("os", OSInfo{})
//
// The structure is defined once: change the type and the contract follows, so the declaration cannot
// drift from reality. When there genuinely is no structure, use Object (it is an object, but the keys
// are not ours) or Any (not even the kind is known).
func Json(name string, shape any) *B {
	b := mk(name, contract.TJSON)
	t := reflect.TypeOf(shape)
	// map[string]T: keys known only at runtime, values all one type — that is valueType, not fields.
	// Passing map[string]float64{} is the natural way to write "field name -> weight", and not
	// recognising it would force the author back to opaque.
	if t != nil && t.Kind() == reflect.Map && t.Key().Kind() == reflect.String {
		el := t.Elem()
		if el.Kind() != reflect.Interface {
			vt := &contract.Field{Type: scalarType(el), Fields: contract.DeriveFields(el), GoType: goTypeName(el)}
			if el.Kind() == reflect.Struct {
				vt.Type = contract.TJSON
			}
			if n := intKindOf(el); n != "" {
				vt.GoType = n
			}
			b.f.ValueType = vt
			return b
		}
		b.f.Opaque = true // map[string]any: there genuinely is no structure
		return b
	}
	b.f.Fields = contract.DeriveFields(t)
	b.f.GoType = goTypeName(t) // generation reuses this type instead of rebuilding an equivalent one
	return b
}

// intKindOf returns the integer type name, or "" for anything that is not an integer.
func intKindOf(t reflect.Type) string {
	switch t.Kind() {
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64,
		reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64:
		return t.Kind().String()
	}
	return ""
}

// Array is a slice whose **element type comes from the slice you pass**:
//
//	field.Array("hosts", []string{})   // scalar elements
//	field.Array("blocks", []Block{})   // structured elements
//
// One parameter covers both cases: scalars become ItemType, structures become Fields.
func Array(name string, shape any) *B {
	b := mk(name, contract.TArray)
	t := reflect.TypeOf(shape)
	if t == nil || (t.Kind() != reflect.Slice && t.Kind() != reflect.Array) {
		// Given something that is not a slice: leave it empty rather than guess. Running the schema at
		// generation time exposes the wrong contract.
		return b
	}
	el := t.Elem()
	for el.Kind() == reflect.Pointer {
		el = el.Elem()
	}
	switch el.Kind() {
	case reflect.Struct:
		b.f.Fields = contract.DeriveFields(el)
		b.f.GoType = goTypeName(el)
	case reflect.Map, reflect.Interface:
		b.f.Opaque = true // []map[string]any or []any: there genuinely is no structure
	default:
		b.f.ItemType = scalarType(el)
	}
	return b
}

// Shortcuts for the common scalar arrays.
func Strings(name string) *B { return Array(name, []string{}) }
func Numbers(name string) *B { return Array(name, []float64{}) }

// Ints is an array of integers.
func Ints(name string) *B  { b := Array(name, []int{}); b.f.GoType = "int"; return b }
func Bools(name string) *B { return Array(name, []bool{}) }

// OneOf declares that a field accepts more than one possibility. **Scalar types and Go types can
// both be passed and are sorted out automatically**:
//
//	field.OneOf("chat_id", contract.TNumber, contract.TString)   // scalar union: the value is a number or a string
//	field.OneOf("doc", DocObject{}, BlocksArray{})     // structural union: different shapes, each with its own fields
//	field.OneOf("x", contract.TString, DocObject{})         // mixing the two is fine
//
// Why the declaration has one concept while the contract has two fields (Types and OneOf): the
// frontend **renders them differently** — a scalar union is one input that accepts several types, a
// structural union is a segmented control that picks a shape first and then renders that branch's
// fields. Storing them apart means the UI never has to guess from "does this branch have fields".
// The author should not be distracted by that difference, so the declaration merges them.
//
// Structural branches use **real types**: renaming or deleting one fails to compile immediately.
func OneOf(name string, variants ...any) *B {
	b := mk(name, contract.TJSON)
	for _, v := range variants {
		// A scalar type goes into Types, the first one being the primary type
		if pt, ok := v.(contract.ParamType); ok {
			if len(b.f.Types) == 0 {
				b.f.Type = pt
			}
			b.f.Types = append(b.f.Types, pt)
			continue
		}
		t := reflect.TypeOf(v)
		if t == nil {
			continue
		}
		b.f.OneOf = append(b.f.OneOf, variantOf(t))
	}
	return b
}

// ArrayOf declares an array whose elements are a union: []OneOf<A, B, …>.
//
// The parts of a multimodal message have exactly this shape: each segment may be text or an image.
// Without it the only options are a single element type (losing the other shapes) or Opaque, which is
// precisely what this avoids.
//
// It adds no field to the contract: type=array plus oneOf means "the **elements** are a union", as
// distinct from OneOf's type=json plus oneOf, which means "the **field itself** is a union".
func ArrayOf(name string, variants ...any) *B {
	b := mk(name, contract.TArray)
	for _, v := range variants {
		t := reflect.TypeOf(v)
		if t == nil {
			continue
		}
		b.f.OneOf = append(b.f.OneOf, variantOf(t))
	}
	return b
}

// Any declares any JSON value: not even whether it is an object is known.
//
// The difference from Object is worth remembering. Object means "it **is** an object, but the keys
// are not ours" (map[string]any, and validation insists on an object); Any means "it may be an
// object, an array, a string, a number or a boolean" (any, and validation lets everything through).
// An HTTP body is the latter: an object in json mode, a string in raw mode.
//
// It is implemented with the existing union, since any is the union of all types. Validation accepts
// anything, generation produces any, and the audit still sees the reason — three things with no new
// mechanism. The reason is required here too.
func Any(name, reason string) *B {
	b := mk(name, contract.TJSON)
	b.f.Types = []contract.ParamType{contract.TJSON, contract.TArray, contract.TString, contract.TNumber, contract.TBool}
	b.f.Opaque = true
	b.f.Desc = reason
	return b
}

// Object declares "this is an object, but its keys are not decided by this plugin" — metadata passed
// through from upstream, say.
//
// The difference from Json is where the structure comes from: Json takes it from a Go type, Object
// has none to take. For the difference from Any, see Any.
//
// **The reason is required.** What stops casual use is not the name but that parameter: taking the
// shortcut still means writing down why the shortcut cannot be avoided. Object and Any together are
// the contract's opaque fields, and the audit lists every one of them.
func Object(name, reason string) *B {
	b := mk(name, contract.TJSON)
	b.f.Opaque = true
	b.f.Desc = reason
	return b
}

// variantOf turns a Go type into a oneOf branch.
//
// An array branch's GoType is the **element** type: an anonymous slice has no name of its own
// ([]Block{}.Name() is ""), and generated code wants exactly the element type ([]schema.Block). The
// display name falls back to "element name + List".
func variantOf(t reflect.Type) contract.OneOfVariant {
	for t.Kind() == reflect.Pointer {
		t = t.Elem()
	}
	if t.Kind() == reflect.Slice || t.Kind() == reflect.Array {
		el := t.Elem()
		for el.Kind() == reflect.Pointer {
			el = el.Elem()
		}
		name := t.Name() // a named slice (type BlocksArray []Block) does have a name
		if name == "" {
			name = el.Name() + "List"
		}
		return contract.OneOfVariant{Name: name, GoType: el.Name(), Type: contract.TArray, Fields: contract.DeriveFields(el)}
	}
	return contract.OneOfVariant{Name: t.Name(), GoType: t.Name(), Type: contract.TJSON, Fields: contract.DeriveFields(t)}
}

// goTypeName returns a named type's name; an anonymous struct has none, and generation inlines it.
func goTypeName(t reflect.Type) string {
	if t == nil {
		return ""
	}
	for t.Kind() == reflect.Pointer {
		t = t.Elem()
	}
	return t.Name()
}

// scalarType maps a Go scalar kind to a contract type.
func scalarType(t reflect.Type) contract.ParamType {
	switch t.Kind() {
	case reflect.Bool:
		return contract.TBool
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64,
		reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64,
		reflect.Float32, reflect.Float64:
		return contract.TNumber
	}
	return contract.TString
}

func (b *B) Label(s string) *B { b.f.Label = s; return b }
func (b *B) Desc(s string) *B  { b.f.Desc = s; return b }
func (b *B) Required() *B      { b.f.Required = true; return b }

// Opaque admits outright that there is no structure to declare here, and says why.
//
// The only difference from field.Object/Any is **where it applies**: those two know at construction
// that there is no structure, while this one is for a constructor that already fixed the shape (Array,
// say) but genuinely cannot describe the elements — an array operation's output, whose element shape
// follows whatever came in and is known only at runtime.
//
// The reason is required: a structureless field without one gets chased by sokel-gen's audit, which
// is the point.
func (b *B) Opaque(reason string) *B {
	b.f.Opaque = true
	if b.f.Desc == "" {
		b.f.Desc = reason
	}
	return b
}
func (b *B) Optional() *B { b.f.Required = false; return b }

// Default sets a default value, which makes the field optional: it runs without the caller passing one.
func (b *B) Default(v any) *B { b.f.Default = v; b.f.Required = false; return b }

// Opt builds one candidate: pass just the value when it reads fine on its own, add a display name
// when the value is a code.
func Opt(value string, label ...string) contract.Option {
	o := contract.Option{Value: value}
	if len(label) > 0 {
		o.Label = label[0]
	}
	return o
}

// Types declares a top-level scalar union such as number|string. Structural unions are OneOf.
func (b *B) Types(ts ...contract.ParamType) *B {
	if len(ts) > 0 {
		b.f.Type = ts[0]
		b.f.Types = ts
	}
	return b
}
