// Copyright 2026 The Sokel Authors
// SPDX-License-Identifier: Apache-2.0

package field_test

import (
	"encoding/json"
	"reflect"
	"strings"
	"testing"

	"github.com/sokel-dev/sokel-plugin-sdk/contract"
	"github.com/sokel-dev/sokel-plugin-sdk/contract/field"
)

// Try the feel of it on a real sysinfo operation: a declaration has to be clearer than struct+tag, not
// merely more verbose for no reason.
type fileDigest struct{}

func (fileDigest) Meta() contract.Meta {
	return contract.Meta{ID: "file_digest", Label: "File digest", Desc: "compute the md5 and size of the input file"}
}
func (fileDigest) Inputs() []contract.FieldSpec {
	return []contract.FieldSpec{
		field.File("file").Label("File").Desc("any file; its md5 and size are computed"),
	}
}
func (fileDigest) Outputs() []contract.FieldSpec {
	return []contract.FieldSpec{
		field.String("filename").Label("File name"),
		field.String("md5").Label("MD5"),
		field.Number("size").Label("Bytes"),
	}
}

func TestSchemaToOperation(t *testing.T) {
	op := contract.OperationOf(fileDigest{})
	if op.ID != "file_digest" || op.Label != "File digest" {
		t.Errorf("meta did not come through: %+v", op)
	}
	if len(op.Inputs) != 1 || op.Inputs[0].Type != contract.TFile || !op.Inputs[0].Required {
		t.Errorf("inputs are wrong; they should be required by default: %+v", op.Inputs)
	}
	if len(op.Outputs) != 3 || op.Outputs[2].Type != contract.TNumber {
		t.Errorf("outputs are wrong: %+v", op.Outputs)
	}
}

// A declaration is plain data: serialise it and hand it to another language's generator (the point
// being Go declaration -> JSON -> many languages).
func TestSchemaIsSerializable(t *testing.T) {
	b, err := json.Marshal(contract.OperationOf(fileDigest{}))
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{`"file_digest"`, `"file"`, `"md5"`, `"number"`} {
		if !strings.Contains(string(b), want) {
			t.Errorf("the exported JSON is missing %s: %s", want, b)
		}
	}
}

// What the various builders mean: required by default, a Default implying optional, enum labels, array
// element types, oneOf taking real types, and opaque demanding a reason.
func TestBuilderSemantics(t *testing.T) {
	f := func(b contract.FieldSpec) contract.Field { return b.Field() }

	if !f(field.String("a")).Required {
		t.Error("required by default: forgetting to write Required must not quietly make a required field optional")
	}
	if f(field.String("a").Optional()).Required {
		t.Error("Optional should take effect")
	}
	if d := f(field.Number("n").Default(3)); d.Required || d.Default != 3 {
		t.Errorf("a default implies optional: %+v", d)
	}
	if o := f(field.Enum("v", field.Opt("xiaoyan", "Xiaoyan"), field.Opt("asc"))); len(o.Options) != 2 ||
		o.Options[0].Label != "Xiaoyan" || o.Options[1].Label != "" {
		t.Errorf("an enum label is optional: %+v", o.Options)
	}
	if a := f(field.Strings("names")); a.ItemType != contract.TString {
		t.Errorf("a scalar element type must be expressible ([]string vs []number): %+v", a)
	}
	if n := f(field.Json("cfg", struct {
		Count float64 `sokel:"count"`
	}{})); len(n.Fields) != 1 || n.Fields[0].Type != contract.TNumber {
		t.Errorf("nested struct: %+v", n)
	}

	// opaque demands a reason, and the reason lands in desc, so a reviewer can see why structure was given
	// up here
	op := f(field.Object("blob", "passed through from outside; the shape is upstream's to decide"))
	if !op.Opaque || !strings.Contains(op.Desc, "upstream") {
		t.Errorf("Opaque should record its reason: %+v", op)
	}
}

// The type is the constructor argument: structure comes from the Go type rather than the fields being
// transcribed. The struct is defined once, so the contract follows whenever the type changes and
// "declaration out of step with the actual type" cannot happen.
func TestShapeFromType(t *testing.T) {
	type osInfo struct {
		Name string `sokel:"name" label:"OS name"`
		Arch string `sokel:"arch"`
		Cpus int    `sokel:"cpus"`
	}
	type block struct {
		Text string `sokel:"text"`
	}

	j := field.Json("os", osInfo{}).Field()
	if len(j.Fields) != 3 {
		t.Fatalf("json should take the type's fields: %+v", j.Fields)
	}
	if j.Fields[0].Name != "name" || j.Fields[0].Label != "OS name" {
		t.Errorf("tag semantics should be preserved: %+v", j.Fields[0])
	}
	if j.Fields[2].Type != contract.TNumber {
		t.Errorf("type mapping: %+v", j.Fields[2])
	}

	a := field.Array("blocks", []block{}).Field()
	if a.Type != contract.TArray || len(a.Fields) != 1 || a.Fields[0].Name != "text" {
		t.Errorf("the array's element structure: %+v", a)
	}
}

// The point of this API is that **nothing can be omitted**: everything needed sits in an argument
// position and the compiler enforces it. The chained version's hole was that `field.Json("os")`
// compiled with nothing added and quietly produced a structureless json; now declaring a json requires
// handing over a type, and going weakly typed requires Opaque with a reason.
func TestTypeIsMandatory(t *testing.T) {
	type osInfo struct {
		Name string `sokel:"name"`
	}

	// json requires a type, so the structure is always there
	j := field.Json("os", osInfo{}).Field()
	if len(j.Fields) == 0 || j.Opaque {
		t.Errorf("Json should carry structure and not be opaque: %+v", j)
	}

	// Arrays: one argument covers both scalar and struct elements, so there are not two methods to
	// remember
	if s := field.Array("hosts", []string{}).Field(); s.ItemType != contract.TString || len(s.Fields) != 0 {
		t.Errorf("scalar elements land in ItemType: %+v", s)
	}
	if o := field.Array("rows", []osInfo{}).Field(); len(o.Fields) != 1 || o.ItemType != "" {
		t.Errorf("struct elements land in Fields: %+v", o)
	}
	// []map[string]any genuinely has no structure, so mark it opaque honestly rather than leave an empty
	// shell
	if m := field.Array("blobs", []map[string]any{}).Field(); !m.Opaque {
		t.Errorf("[]map[string]any should be marked opaque: %+v", m)
	}

	// The only way into weak typing, with the reason in an argument position where it cannot be omitted
	if op := field.Object("blob", "passed through from upstream").Field(); !op.Opaque || op.Desc != "passed through from upstream" {
		t.Errorf("an Opaque reason is mandatory: %+v", op)
	}

	// A oneOf variant is a real type and its name comes from that type, so a misspelled type name is a
	// compile error
	type docObject struct {
		Title string `sokel:"title"`
	}
	type blocks []osInfo
	u := field.OneOf("doc", docObject{}, blocks{}).Field()
	if len(u.OneOf) != 2 {
		t.Fatalf("the oneOf should have 2 branches: %+v", u.OneOf)
	}
	if u.OneOf[0].Name != "docObject" || u.OneOf[0].Type != contract.TJSON {
		t.Errorf("branch 0: %+v", u.OneOf[0])
	}
	if u.OneOf[1].Type != contract.TArray || len(u.OneOf[1].Fields) != 1 {
		t.Errorf("a named slice branch should be an array with its elements expanded: %+v", u.OneOf[1])
	}
}

// The Go type name given at declaration time has to survive into the contract: generating the In/Out
// structs **reuses** that type rather than rebuilding an identically shaped one from Fields —
// converting between two equivalent types is exactly the runtime conversion this eliminates.
func TestGoTypeRecorded(t *testing.T) {
	type osInfo struct {
		Name string `sokel:"name"`
	}
	if j := field.Json("os", osInfo{}).Field(); j.GoType != "osInfo" {
		t.Errorf("json should remember the type name: %+v", j)
	}
	if a := field.Array("rows", []osInfo{}).Field(); a.GoType != "osInfo" {
		t.Errorf("array elements should remember the type name too: %+v", a)
	}
	// An anonymous struct has no name and can only be expanded inline, so leave it empty rather than
	// invent one
	anon := field.Json("cfg", struct {
		X int `sokel:"x"`
	}{}).Field()
	if anon.GoType != "" {
		t.Errorf("an anonymous struct should have no type name: %+v", anon)
	}
	// A scalar array has no use for a type name
	if s := field.Strings("hosts").Field(); s.GoType != "" {
		t.Errorf("a scalar array should have no type name: %+v", s)
	}
}

// One OneOf concept covers both kinds of union: pass a ParamType for scalars, a Go type for structs, or
// mix them. The author never has to work out "is this Types or OneOf" — that distinction exists only so
// the contract layer saves the UI a guess.
func TestOneOfUnifiesScalarAndStruct(t *testing.T) {
	type docObject struct {
		Title string `sokel:"title"`
	}

	// A scalar union lands in Types, the first being the primary type
	sc := field.OneOf("chat_id", contract.TNumber, contract.TString).Field()
	if sc.Type != contract.TNumber || len(sc.Types) != 2 || len(sc.OneOf) != 0 {
		t.Errorf("a scalar union should land in Types: %+v", sc)
	}

	// A structural union lands in OneOf, with branch names taken from the real types
	st := field.OneOf("doc", docObject{}, []docObject{}).Field()
	if len(st.OneOf) != 2 || len(st.Types) != 0 {
		t.Fatalf("a structural union should land in OneOf: %+v", st)
	}
	if st.OneOf[0].Name != "docObject" || st.OneOf[0].Type != contract.TJSON {
		t.Errorf("object branch: %+v", st.OneOf[0])
	}
	if st.OneOf[1].Type != contract.TArray || len(st.OneOf[1].Fields) != 1 {
		t.Errorf("a slice branch should be an array with its elements expanded: %+v", st.OneOf[1])
	}

	// Mixed: each goes where it belongs
	mx := field.OneOf("x", contract.TString, docObject{}).Field()
	if len(mx.Types) != 1 || len(mx.OneOf) != 1 {
		t.Errorf("mixing should keep each in its place: types=%v oneOf=%v", mx.Types, mx.OneOf)
	}
}

// Integers: the contract type stays number, so the wire protocol is unchanged and the platform is
// unaffected, and GoType carries a "this is really an integer" hint for the generator. That way an int
// such as runtime.NumCPU() needs no float64(...) conversion in the implementation. The distinction is
// deliberately confined to the plugin side: the platform's type system has no notion of int, and
// changing the wire protocol for one is not worth it.
func TestIntStaysNumberInContract(t *testing.T) {
	i := field.Int("count").Field()
	if i.Type != contract.TNumber {
		t.Errorf("the contract type should still be number; the platform knows no int: %+v", i)
	}
	if i.GoType != "int" {
		t.Errorf("the integer hint should be present: %+v", i)
	}
	if s := field.Ints("sizes").Field(); s.Type != contract.TArray || s.ItemType != contract.TNumber || s.GoType != "int" {
		t.Errorf("integer array: %+v", s)
	}
	// Floats carry no hint and stay as they are
	if f := field.Number("ratio").Field(); f.GoType != "" {
		t.Errorf("a float should carry no integer hint: %+v", f)
	}
}

// Deriving from a Go type must recognise integer fields too — Shape and Json go through reflection.
func TestDeriveIntFromStruct(t *testing.T) {
	type row struct {
		N     int     `sokel:"n"`
		Big   int64   `sokel:"big"`
		Ratio float64 `sokel:"ratio"`
	}
	fs := contract.DeriveFields(reflect.TypeOf(row{}))
	idx := map[string]contract.Field{}
	for _, f := range fs {
		idx[f.Name] = f
	}
	if idx["n"].Type != contract.TNumber || idx["n"].GoType != "int" {
		t.Errorf("int field: %+v", idx["n"])
	}
	if idx["big"].GoType != "int64" {
		t.Errorf("int64 should keep its width; generating an int would lose precision: %+v", idx["big"])
	}
	if idx["ratio"].GoType != "" {
		t.Errorf("float64 should carry no integer hint: %+v", idx["ratio"])
	}
}

// Passing map[string]T to field.Json is a natural thing to write ("field name -> weight" and the like),
// and it is a valueType rather than fields. Not recognising it would fall back to opaque, throwing away
// a value type that was already known.
func TestJsonWithMapShape(t *testing.T) {
	m := field.Json("field_boosts", map[string]float64{}).Field()
	if m.ValueType == nil || m.ValueType.Type != contract.TNumber {
		t.Errorf("map[string]float64 should land in valueType(number): %+v", m)
	}
	if m.Opaque {
		t.Errorf("a known value type should not be opaque: %+v", m)
	}
	// map[string]any genuinely has no structure
	if a := field.Json("blob", map[string]any{}).Field(); !a.Opaque {
		t.Errorf("map[string]any should be opaque: %+v", a)
	}
	// A struct value expands recursively
	type row struct {
		N string `sokel:"n"`
	}
	r := field.Json("rows", map[string]row{}).Field()
	if r.ValueType == nil || len(r.ValueType.Fields) != 1 {
		t.Errorf("map[string]struct should expand recursively: %+v", r.ValueType)
	}
}

// Array elements that are a union: `[]OneOf<text, image>`. A multimodal message's parts have exactly
// this shape — each segment of one message may be text or an image. No new contract field is needed:
// type=array with oneOf means "the elements are a union", distinct from type=json with oneOf, where the
// field itself is the union.
func TestArrayOfUnion(t *testing.T) {
	type textPart struct {
		Text string `sokel:"text"`
	}
	type imagePart struct {
		URL string `sokel:"url"`
	}

	f := field.ArrayOf("parts", textPart{}, imagePart{}).Label("Parts").Field()
	if f.Type != contract.TArray {
		t.Fatalf("should be an array: %+v", f)
	}
	if len(f.OneOf) != 2 {
		t.Fatalf("the elements should have 2 branches: %+v", f.OneOf)
	}
	if f.OneOf[0].Name != "textPart" || len(f.OneOf[0].Fields) != 1 {
		t.Errorf("branch 0 should expand its structure: %+v", f.OneOf[0])
	}
	if f.OneOf[1].GoType != "imagePart" {
		t.Errorf("a branch should remember its Go type name, which generation needs: %+v", f.OneOf[1])
	}
	// Elements with a declared structure are not opaque
	if f.Opaque {
		t.Errorf("elements that declare a shape should not be opaque: %+v", f)
	}
	// As distinct from the field itself being a union, which is json
	if u := field.OneOf("doc", textPart{}).Field(); u.Type != contract.TJSON {
		t.Errorf("a field-level union is still json: %+v", u)
	}
}

// "Any JSON value": an http request or response body has exactly this shape — an object in json mode, a
// string in raw mode, possibly an array. Opaque cannot express it, since that means "an object with
// open-ended keys".
//
// The existing union type expresses it most naturally: any is the union of all types. Validation then
// admits any value, generation emits any, and the audit still sees the reason — three things, no new
// mechanism.
func TestAny(t *testing.T) {
	f := field.Any("body", "the body's shape is the target API's to decide; the platform does not intervene").Label("Body").Field()
	if f.Type != contract.TJSON {
		t.Errorf("the primary type is still json, which is how the canvas renders it: %v", f.Type)
	}
	if len(f.Types) < 4 {
		t.Errorf("it should expand into a union of every type: %v", f.Types)
	}
	var hasString, hasArray bool
	for _, ty := range f.Types {
		hasString = hasString || ty == contract.TString
		hasArray = hasArray || ty == contract.TArray
	}
	if !hasString || !hasArray {
		t.Errorf("both string and array belong in the union; raw mode sends a string: %v", f.Types)
	}
	if !f.Opaque || f.Desc == "" {
		t.Errorf("it is still an opaque field and the reason is mandatory, or it is indistinguishable from a forgotten declaration: %+v", f)
	}
}

// field.Array's second argument is **the element shape**, not descriptive text.
//
// Passing a description silently yields a structureless array: downstream has no idea what messages[0]
// contains, the variable picker cannot expand it and references go unchecked. One version of the gmail
// plugin shipped exactly that, and nothing anywhere raised an error.
func TestArrayShapeVsDescription(t *testing.T) {
	type Item struct {
		ID      string `sokel:"id"`
		Subject string `sokel:"subject"`
	}
	withShape := field.Array("messages", []Item{}).Field()
	if len(withShape.Fields) != 2 || withShape.Fields[0].Name != "id" {
		t.Fatalf("given an element shape there should be element fields: %+v", withShape.Fields)
	}
	// Passing descriptive text produces no structure, which is exactly the form to be caught elsewhere
	withDesc := field.Array("messages", "the mail list").Field()
	if len(withDesc.Fields) != 0 {
		t.Errorf("a non-slice should not have structure guessed for it: %+v", withDesc.Fields)
	}
	// Scalar elements record an ItemType, so downstream knows it is an array of strings rather than of
	// objects
	strs := field.Array("label_ids", []string{}).Field()
	if strs.ItemType != contract.TString {
		t.Errorf("scalar elements must record an ItemType, got %q", strs.ItemType)
	}
	// []map[string]any and []any genuinely have no structure, so opaque: structurelessness with a reason,
	// not a missing declaration
	opaque := field.Array("blocks", []map[string]any{}).Field()
	if !opaque.Opaque {
		t.Error("[]map[string]any should be marked opaque")
	}
}
