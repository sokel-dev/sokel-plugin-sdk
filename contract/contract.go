// Copyright 2026 The Sokel Authors
// SPDX-License-Identifier: Apache-2.0

// Package contract is the **single definition** of a plugin contract: field types, declarations, and
// the binding between them and Go types.
//
// Why a package of its own rather than part of the SDK: a contract has two consumers —
//   - the SDK, where plugin authors declare it, bind inputs by it and produce outputs by it;
//   - the platform, which normalises types and validates before a run, and renders the canvas from it.
//
// The two used to **define it separately**: the SDK's Field was the complete one while the
// platform's had only name/type/fields/valueType. Anything the SDK declared that the other lacked —
// unions, enums, required, oneOf, multiple — was invisible to the platform. That "one thing defined
// in two places" drift has already cost once: input binding was asymmetric between the two sides, and
// nested snake_case fields bound to nothing at all.
//
// This package depends on neither transport nor platform types: a contract is data, and fetching
// bytes or sending requests does not belong here.
package contract

import (
	"encoding/json"
	"reflect"
	"strconv"
	"strings"
)

// ParamType is the field type, matching the platform and the frontend, which render input and output
// binding from it.
type ParamType string

const (
	TString ParamType = "string"
	TNumber ParamType = "number"
	TBool   ParamType = "boolean"
	TFile   ParamType = "file"
	TJSON   ParamType = "json"
	TArray  ParamType = "array"
	TEnum   ParamType = "enum"
)

// Field is one input/output item of an operation contract.
type Field struct {
	Name     string      `json:"name"`
	Label    string      `json:"label,omitempty"`
	Type     ParamType   `json:"type"`
	Types    []ParamType `json:"types,omitempty"` // a union such as number|string: binding and validation accept any of them, Type being the primary
	Required bool        `json:"required,omitempty"`
	Default  any         `json:"default,omitempty"`
	Desc     string      `json:"desc,omitempty"`
	Options  []Option    `json:"options,omitempty"` // enum candidates, optionally with display names
	Fields   []Field     `json:"fields,omitempty"`  // sub-fields of a json field, or element fields of an array

	// OneOf is a structural union: the field accepts one of the listed structures.
	// **Runtime reflection cannot produce it** — Go has no union type, and reflection cannot map a type
	// name back to a type. It comes from the declaration, read at generation time.
	OneOf []OneOfVariant `json:"oneOf,omitempty"`
	// ValueType covers dynamic keys (JSON Schema's additionalProperties): the keys are known only at
	// runtime and every value shares one type. Derived from map[string]T — an any T produces nothing
	// (opaque), a concrete T expands recursively. Mutually exclusive with Fields.
	ValueType *Field `json:"valueType,omitempty"`
	// GoType is the Go type name given at declaration time ("OSInfo", say). It is **only a hint for
	// code generation**; generators for other languages ignore it, and protocol consumers do not need it.
	//
	// Why it has to be recorded: field.Json("os", OSInfo{}) already handed over the type, so generating
	// the Out struct should reuse OSInfo itself rather than rebuild an equivalent structure from Fields.
	// Otherwise the implementation ends up with two identically shaped types and every assignment needs
	// a field-by-field conversion — exactly the runtime conversion this design exists to remove.
	GoType string `json:"goType,omitempty"`
	// ItemType is the scalar element type of an array. []string and []number have to be
	// distinguishable in the contract, and Fields can only express "the fields of an object element",
	// so scalar elements previously had nowhere to live.
	ItemType ParamType `json:"itemType,omitempty"`
	// Opaque means the field has no structure to declare (a bare map[string]any or any).
	// Weak typing is a legitimate choice, but it should be a **visible decision** rather than the
	// default path: the UI marks such a field as unconstrained and the platform skips structural
	// validation for it. Without the flag, "no structure was declared" and "a structure was declared
	// and happens to be empty" are indistinguishable.
	Opaque bool `json:"opaque,omitempty"`
}

// Option is one enum candidate. With an empty Label the frontend falls back to showing Value: when
// the value reads fine on its own (asc/desc) there is nothing to repeat, and only a value that is a
// code needs a display name.
type Option struct {
	Value string `json:"value"`
	Label string `json:"label,omitempty"`
}

// OneOfVariant is one branch of a oneOf. Name identifies the branch for error messages and never
// appears in the runtime value: that value *is* the branch's own shape, with no discriminator wrapper.
type OneOfVariant struct {
	Name  string    `json:"name"`
	Label string    `json:"label,omitempty"`
	Type  ParamType `json:"type"`
	// GoType is the branch's Go type name — for an array branch, the **element** type ([]Block ->
	// "Block"). It is separate from Name because an anonymous slice has no name:
	// reflect.TypeOf([]Block{}).Name() returns "", and using Name alone generated broken code like
	// `[]schema.` (observed in practice).
	GoType string  `json:"goType,omitempty"`
	Fields []Field `json:"fields,omitempty"`
}

// FileRef marks a type as a platform file reference; the SDK's File implements it.
//
// The contract package only needs to **recognise** a file field — report type=file and let the
// standard library bind it. Fetching bytes and uploading are runtime concerns that belong to the SDK,
// and recognising one field should not drag the runtime in here.
type FileRef interface{ FileRef() }

var fileRefType = reflect.TypeOf((*FileRef)(nil)).Elem()

// isFileRef reports whether t is a file reference; both *File and File count.
func isFileRef(t reflect.Type) bool {
	if t == nil {
		return false
	}
	if t.Implements(fileRefType) {
		return true
	}
	return t.Kind() != reflect.Pointer && reflect.PointerTo(t).Implements(fileRefType)
}

// DeriveFields derives contract fields from a Go type.
//
// Using reflection here does not contradict "no reflection at runtime": it runs at **declaration
// time** — the moment sokel-gen executes the schema to read the declaration — and its product is a
// static Field literal in generated code. The problem with reflection was never reflection itself; it
// was reflection at runtime.
func DeriveFields(t reflect.Type) []Field { return deriveFields(t) }

// deriveFields derives contract fields from an input/output struct by reflection.
//
//	tags: `sokel:"name,optional"` plus separate `label:"..."`, `desc:"..."`, `default:"..."`.
//	types: bool->boolean, integer/float->number, string->string, *File->file, slice->array,
//	struct/map->json (with sub-fields derived recursively).
func deriveFields(t reflect.Type) []Field {
	return deriveFieldsSeen(t, map[reflect.Type]bool{})
}

// deriveFieldsSeen carries the set of types already visited on the current recursion path. That set
// is cycle detection: a self-referential struct (a tree such as Block{ Blocks []*Block }) stops on
// re-entering the same type and returns nothing, instead of recursing until the stack overflows —
// which is what once crashed the process while deriving a recursive type at startup.
//
// The set is path-scoped: marked on entry and removed on exit, so the same type in a sibling branch
// is unaffected. This is not global deduplication.
func deriveFieldsSeen(t reflect.Type, seen map[reflect.Type]bool) []Field {
	for t.Kind() == reflect.Pointer {
		t = t.Elem()
	}
	if t.Kind() != reflect.Struct {
		return nil
	}
	if seen[t] {
		return nil // a cycle: this type is already on the current path, so stop descending
	}
	seen[t] = true
	defer delete(seen, t)
	var out []Field
	for i := 0; i < t.NumField(); i++ {
		sf := t.Field(i)
		if !sf.IsExported() {
			continue
		}
		name, optional := parseSokelTag(sf)
		if name == "-" {
			continue
		}
		f := Field{Name: name, Label: sf.Tag.Get("label"), Desc: sf.Tag.Get("desc")}
		f.Type, f.Fields, f.ValueType = fieldTypeSeen(sf.Type, seen)
		// `opaque:"reason"` lets a structureless field inside a type definition carry its reason too.
		// Otherwise only the builder's field.Object could state one, and a map[string]any inside a struct
		// would become a black hole nobody explains.
		taggedOpaque := false
		if rv, ok := sf.Tag.Lookup("opaque"); ok && strings.TrimSpace(rv) != "" {
			taggedOpaque = true
			if f.Desc == "" {
				f.Desc = strings.TrimSpace(rv)
			}
		}
		// Integers: the contract type is number, and GoType remembers that it is really an integer
		// (and how wide).
		if f.Type == TNumber && f.GoType == "" {
			f.GoType = intKindName(sf.Type)
		}
		// `type:"..."` names the contract type explicitly, overriding what reflection derived.
		// Comma-separated means a union (`type:"number,string"` for an id that may be either): the first
		// is the primary type used by display and fixed-value controls, and all of them go into Types,
		// where binding and validation accept any one — while still rejecting anything else.
		if tv, ok := sf.Tag.Lookup("type"); ok && strings.TrimSpace(tv) != "" {
			var ts []ParamType
			for _, p := range strings.Split(tv, ",") {
				if p = strings.TrimSpace(p); p != "" {
					ts = append(ts, ParamType(p))
				}
			}
			if len(ts) > 0 {
				f.Type = ts[0]
				if len(ts) > 1 {
					f.Types = ts
				}
			}
		}
		// `enum:"a,b,c"` turns a string field into an enum with candidates; the canvas renders a
		// dropdown and restricts the value. `enum:"a,b"` gives bare values, `enum:"a=A,b=B"` adds display
		// names. The separator is = rather than : because a colon inside an enum value (a URL, a time) is
		// far more common than an equals sign.
		if ev, ok := sf.Tag.Lookup("enum"); ok && strings.TrimSpace(ev) != "" {
			f.Type = TEnum
			for _, o := range strings.Split(ev, ",") {
				if o = strings.TrimSpace(o); o == "" {
					continue
				}
				val, label, _ := strings.Cut(o, "=")
				f.Options = append(f.Options, Option{
					Value: strings.TrimSpace(val),
					Label: strings.TrimSpace(label),
				})
			}
		}
		// The opaque decision must come **after** the type and enum tags: they rewrite f.Type, and
		// whether there is structure depends on the final type. It used to come first, so an `any` field
		// with `type:"number,string"` — a scalar union, an id that may be a number or a string — was
		// wrongly marked opaque. The opaque audit caught it on its first run.
		//
		// No structure to declare means opaque, and it arrives two ways:
		//   json  — a bare map, any, or an empty struct
		//   array — elements without structure ([]map[string]any, []any, or a named map alias in a slice)
		//
		// An array of scalars ([]string) is not opaque: the element type is definite and goes into
		// ItemType. This path used to miss that — the builder filled ItemType and reflection did not — and
		// the consequence was a "structureless array" downstream, indistinguishable from a genuine []any.
		if f.Type == TArray && len(f.Fields) == 0 {
			f.ItemType = elemScalarType(sf.Type)
		}
		// An explicit opaque tag wins. The automatic decision recognises only bare maps, any, and
		// structureless elements; it cannot recognise **recursion** ([]*Block containing []*Block, where
		// cycle detection empties the sub-fields even though the type does have structure the contract
		// cannot express). This assignment used to be unconditional, which wiped out the tag and its
		// reason together.
		f.Opaque = taggedOpaque ||
			((f.Type == TJSON || f.Type == TArray) && len(f.Fields) == 0 && f.ValueType == nil &&
				f.ItemType == "" && (f.Type == TJSON || elemHasNoStructure(sf.Type)))
		if dv, ok := sf.Tag.Lookup("default"); ok {
			f.Default = coerceDefault(dv, f.Type)
		}
		// Required by default: not optional, no default, and not a pointer.
		// Files are the exception: a file parameter can only be declared as *File (a value type would be
		// read as a json struct), so "pointer means optional" would make a required file impossible to
		// express. A file therefore relies on an explicit ,optional instead.
		f.Required = !optional && f.Default == nil && (sf.Type.Kind() != reflect.Pointer || f.Type == TFile)
		out = append(out, f)
	}
	return out
}

// elemScalarType returns a slice's scalar element type ([]string->string, []int->number,
// []bool->boolean). A non-scalar element (a struct, a map, any) returns "": those either have Fields
// or genuinely have no structure.
func elemScalarType(t reflect.Type) ParamType {
	for t.Kind() == reflect.Pointer {
		t = t.Elem()
	}
	if t.Kind() != reflect.Slice && t.Kind() != reflect.Array {
		return ""
	}
	el := t.Elem()
	if isFileRef(el) { // []*File or []File: the element type is file, i.e. array<file>
		return TFile
	}
	for el.Kind() == reflect.Pointer {
		el = el.Elem()
	}
	switch el.Kind() {
	case reflect.String:
		return TString
	case reflect.Bool:
		return TBool
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64,
		reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64,
		reflect.Float32, reflect.Float64:
		return TNumber
	}
	return ""
}

// fieldType maps a Go type to a ParamType, deriving sub-fields recursively for json and array, and a
// valueType for a map.
func fieldType(t reflect.Type) (ParamType, []Field, *Field) {
	return fieldTypeSeen(t, map[reflect.Type]bool{})
}

// fieldTypeSeen is the same mapping carrying the visited-type set, which it passes on to
// deriveFieldsSeen for cycle detection. The third return value is the valueType, non-nil only for a
// map[string]T whose T is not any (see Field.ValueType).
func fieldTypeSeen(t reflect.Type, seen map[reflect.Type]bool) (ParamType, []Field, *Field) {
	if isFileRef(t) {
		return TFile, nil, nil
	}
	for t.Kind() == reflect.Pointer {
		t = t.Elem()
	}
	switch t.Kind() {
	case reflect.Bool:
		return TBool, nil, nil
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64,
		reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64,
		reflect.Float32, reflect.Float64:
		return TNumber, nil, nil
	case reflect.String:
		return TString, nil, nil
	case reflect.Slice, reflect.Array:
		// A file array ([]*File or []File) reports array<file>, with ItemType filled in by
		// elemScalarType — the only spelling a file list has. It is deliberately not reflected as a plain
		// struct, which would emit noise fields like i_d, u_r_l and an inline data blob.
		if isFileRef(t.Elem()) {
			return TArray, nil, nil
		}
		return TArray, deriveFieldsSeen(t.Elem(), seen), nil
		// An array's Opaque is decided in deriveFieldsSeen: structureless elements mean the whole array
		// is unconstrained.
	case reflect.Struct:
		return TJSON, deriveFieldsSeen(t, seen), nil
	case reflect.Map:
		// map[string]any becomes opaque (no structural constraint); map[string]T with a concrete T
		// expands recursively into a valueType (keys known only at runtime, values all one type). Go's own
		// type system therefore lines up with the typed/opaque split, with no new concept invented for it.
		// A non-string key cannot be expressed as a JSON object at all, so it is treated as opaque.
		if t.Key().Kind() != reflect.String || t.Elem().Kind() == reflect.Interface {
			return TJSON, nil, nil // a nil valueType is how the caller knows to mark it Opaque
		}
		et, ef, ev := fieldTypeSeen(t.Elem(), seen)
		return TJSON, nil, &Field{Type: et, Fields: ef, ValueType: ev}
	case reflect.Interface:
		return TJSON, nil, nil
	default:
		return TString, nil, nil
	}
}

// parseSokelTag reads a field's external name and optional flag. Without a sokel tag it uses the
// snake_case form of the field name.
// ParseTag is parseSokelTag, exported for the SDK to reuse.
func ParseTag(sf reflect.StructField) (string, bool) { return parseSokelTag(sf) }

// ApplyDefaultTag writes a `default:"..."` into the field, exported for the SDK to reuse.
func ApplyDefaultTag(v reflect.Value, sf reflect.StructField) { applyDefaultTag(v, sf) }

func parseSokelTag(sf reflect.StructField) (name string, optional bool) {
	tag := sf.Tag.Get("sokel")
	if tag == "" {
		return toSnake(sf.Name), false
	}
	parts := strings.Split(tag, ",")
	name = strings.TrimSpace(parts[0])
	if name == "" {
		name = toSnake(sf.Name)
	}
	for _, p := range parts[1:] {
		if strings.TrimSpace(p) == "optional" {
			optional = true
		}
	}
	return name, optional
}

// bindInput binds the platform's input JSON object into an input struct by sokel tag, files included.
// BindInput binds the platform's input JSON into an input struct, **recursively, by sokel tag**.
//
// The recursion is mandatory. The output side (StructToVars) always expanded recursively by sokel
// tag, while the input side once looked only at the top level, so snake_case fields inside a nested
// structure bound to nothing: Go's case-insensitive JSON matching does not cross an underscore, and
// `doc_id` never reaches `DocID` — silently. The two sides have to be each other's inverse.
func BindInput(input json.RawMessage, dst any) error { return bindInput(input, dst) }

func bindInput(input json.RawMessage, dst any) error {
	if len(input) == 0 {
		return nil
	}
	return bindValue(input, reflect.ValueOf(dst).Elem())
}

// bindValue decodes recursively by target type: a struct takes keys by sokel tag, a slice or map
// descends element by element,
// everything else is left to the standard library.
func bindValue(raw json.RawMessage, v reflect.Value) error {
	if len(raw) == 0 || string(raw) == "null" {
		return nil
	}
	switch v.Kind() {
	case reflect.Pointer:
		if v.IsNil() {
			v.Set(reflect.New(v.Type().Elem()))
		}
		return bindValue(raw, v.Elem())

	case reflect.Struct:
		// File references and other platform types whose json tags already line up: leave them to the
		// standard library rather than taking them apart by sokel tag.
		if isFileRef(v.Type()) {
			return json.Unmarshal(raw, v.Addr().Interface())
		}
		var obj map[string]json.RawMessage
		if err := json.Unmarshal(raw, &obj); err != nil {
			return err
		}
		t := v.Type()
		for i := 0; i < t.NumField(); i++ {
			sf := t.Field(i)
			if !sf.IsExported() {
				continue
			}
			name, _ := parseSokelTag(sf)
			val, ok := obj[name]
			if !ok {
				applyDefaultTag(v.Field(i), sf)
				continue
			}
			if err := bindValue(val, v.Field(i)); err != nil {
				return err
			}
		}
		return nil

	case reflect.Slice:
		var items []json.RawMessage
		if err := json.Unmarshal(raw, &items); err != nil {
			return json.Unmarshal(raw, v.Addr().Interface()) // not an array (base64 for []byte, say): standard library
		}
		out := reflect.MakeSlice(v.Type(), len(items), len(items))
		for i, it := range items {
			if err := bindValue(it, out.Index(i)); err != nil {
				return err
			}
		}
		v.Set(out)
		return nil

	case reflect.Map:
		if v.Type().Key().Kind() != reflect.String {
			return json.Unmarshal(raw, v.Addr().Interface())
		}
		var obj map[string]json.RawMessage
		if err := json.Unmarshal(raw, &obj); err != nil {
			return err
		}
		out := reflect.MakeMapWithSize(v.Type(), len(obj))
		for k, val := range obj {
			ev := reflect.New(v.Type().Elem()).Elem()
			if err := bindValue(val, ev); err != nil {
				return err
			}
			out.SetMapIndex(reflect.ValueOf(k), ev)
		}
		v.Set(out)
		return nil
	}
	return json.Unmarshal(raw, v.Addr().Interface())
}

// StructToVars is the exported view of structToVars, for generated code and tests.
func StructToVars(o any) map[string]any { return structToVars(o) }

// structToVars turns an output struct into a {external name: value} map, which the platform stores as
// the node's output variables. Nested structs and slices expand recursively by sokel tag so nested
// field names use contract names too; a *File is carried through as-is, its json tags already aligned.
func structToVars(o any) map[string]any {
	v := reflect.ValueOf(o)
	for v.Kind() == reflect.Pointer {
		if v.IsNil() {
			return nil
		}
		v = v.Elem()
	}
	return structFieldsToMap(v)
}

// structFieldsToMap expands a struct into {external name: value} by sokel tag.
//
// **Nil pointer and interface fields are left out.** First, in streaming a later frame should not
// overwrite an earlier one with an empty value. Second, a typed output lays out every field, and
// something that does not exist on this call should not appear as a null: an HTTP call in file mode
// has no body at all, and emitting body:null only confuses whoever references it downstream.
func structFieldsToMap(v reflect.Value) map[string]any {
	if v.Kind() != reflect.Struct {
		return nil
	}
	t := v.Type()
	m := make(map[string]any, t.NumField())
	for i := 0; i < t.NumField(); i++ {
		sf := t.Field(i)
		if !sf.IsExported() {
			continue
		}
		name, _ := parseSokelTag(sf)
		fv := v.Field(i)
		if (fv.Kind() == reflect.Pointer || fv.Kind() == reflect.Interface) && fv.IsNil() {
			continue
		}
		m[name] = sokelValue(fv)
	}
	return m
}

// sokelValue converts one value by sokel semantics: a nested struct becomes a map (except *File,
// carried through as-is), a slice recurses into []any, everything else stays as it is.
func sokelValue(v reflect.Value) any {
	if isFileRef(v.Type()) {
		return v.Interface() // a file reference: its json tags already match the platform, so keep it
	}
	switch v.Kind() {
	case reflect.Pointer:
		if v.IsNil() {
			return nil
		}
		return sokelValue(v.Elem())
	case reflect.Struct:
		return structFieldsToMap(v)
	case reflect.Slice, reflect.Array:
		out := make([]any, v.Len())
		for i := 0; i < v.Len(); i++ {
			out[i] = sokelValue(v.Index(i))
		}
		return out
	default:
		return v.Interface()
	}
}

func applyDefaultTag(fv reflect.Value, sf reflect.StructField) {
	dv, ok := sf.Tag.Lookup("default")
	if !ok || !fv.CanSet() {
		return
	}
	switch fv.Kind() {
	case reflect.Bool:
		b, _ := strconv.ParseBool(dv)
		fv.SetBool(b)
	case reflect.String:
		fv.SetString(dv)
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
		n, _ := strconv.ParseInt(dv, 10, 64)
		fv.SetInt(n)
	case reflect.Float32, reflect.Float64:
		f, _ := strconv.ParseFloat(dv, 64)
		fv.SetFloat(f)
	}
}

func coerceDefault(dv string, t ParamType) any {
	switch t {
	case TBool:
		b, _ := strconv.ParseBool(dv)
		return b
	case TNumber:
		if f, err := strconv.ParseFloat(dv, 64); err == nil {
			return f
		}
		return dv
	default:
		return dv
	}
}

// toSnake turns a Go field name into snake_case, the fallback when there is no sokel tag. Initialisms
// are cut as one word: ID->id, URL->url, ChatID->chat_id, HTTPCode->http_code. An earlier version
// inserted an underscore before every capital and produced i_d and u_r_l.
func toSnake(s string) string {
	rs := []rune(s)
	var b strings.Builder
	for i, r := range rs {
		if r >= 'A' && r <= 'Z' {
			// A word boundary: the previous character is lowercase or a digit (so chatI|D is not one), or
			// this is the end of a run of capitals followed by a lowercase letter (HTTPC|ode).
			prevLower := i > 0 && (rs[i-1] >= 'a' && rs[i-1] <= 'z' || rs[i-1] >= '0' && rs[i-1] <= '9')
			nextLower := i+1 < len(rs) && rs[i+1] >= 'a' && rs[i+1] <= 'z'
			prevUpper := i > 0 && rs[i-1] >= 'A' && rs[i-1] <= 'Z'
			if prevLower || (prevUpper && nextLower) {
				b.WriteByte('_')
			}
			b.WriteRune(r + 32)
		} else {
			b.WriteRune(r)
		}
	}
	return b.String()
}

// elemHasNoStructure reports whether an array's elements have no structure at all (a bare map, any,
// an interface). It is what marks []map[string]any opaque while leaving []string alone.
func elemHasNoStructure(t reflect.Type) bool {
	for t.Kind() == reflect.Pointer {
		t = t.Elem()
	}
	if t.Kind() != reflect.Slice && t.Kind() != reflect.Array {
		return false
	}
	el := t.Elem()
	if isFileRef(el) { // []*File or []File: the element type is definite (file), so it has structure
		return false
	}
	for el.Kind() == reflect.Pointer {
		el = el.Elem()
	}
	switch el.Kind() {
	case reflect.Interface:
		return true
	case reflect.Map:
		return el.Elem().Kind() == reflect.Interface
	}
	return false
}

// intKindName returns an integer field's Go type name (int, int64, uint32…), or "" for anything else.
// The width is kept rather than flattened to int: narrowing an int64 field to int loses precision.
func intKindName(t reflect.Type) string {
	for t.Kind() == reflect.Pointer {
		t = t.Elem()
	}
	switch t.Kind() {
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64,
		reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64:
		return t.Kind().String()
	}
	return ""
}
