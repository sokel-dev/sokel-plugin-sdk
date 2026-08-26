// Copyright 2026 The Sokel Authors
// SPDX-License-Identifier: Apache-2.0

package contract

import (
	"encoding/json"
	"reflect"
	"testing"
)

// The enum tag: a string field plus `enum:"a,b,c"` becomes Type=enum with Options, and the default is
// kept.
func TestDeriveEnumField(t *testing.T) {
	type In struct {
		Mode string `sokel:"mode,optional" label:"Mode" enum:"html, markdown ,llm_text" default:"markdown"`
		Kind string `sokel:"kind"`
	}
	fields := deriveFields(reflect.TypeOf(new(In)).Elem())
	if len(fields) != 2 {
		t.Fatalf("want 2 fields, got %d", len(fields))
	}
	mode := fields[0]
	if mode.Type != TEnum {
		t.Errorf("mode.Type = %q, want enum", mode.Type)
	}
	if len(mode.Options) != 3 || mode.Options[0].Value != "html" || mode.Options[2].Value != "llm_text" {
		t.Errorf("options parsed wrongly; whitespace should be trimmed: %v", mode.Options)
	}
	if mode.Default != "markdown" {
		t.Errorf("default = %v", mode.Default)
	}
	if mode.Required {
		t.Error("a field with a default should not be required")
	}
	// A plain string field with no enum tag is unaffected.
	if fields[1].Type != TString || len(fields[1].Options) != 0 {
		t.Errorf("kind should be a plain string: %+v", fields[1])
	}
}

// The type tag explicitly overrides the reflected type, e.g. an any that really means number.
func TestFieldTypeOverride(t *testing.T) {
	type In struct {
		ChatID any `sokel:"chat_id" type:"number"` // reflection would say json; overridden to number
		Raw    any `sokel:"raw"`                   // no override, so json
		N      int `sokel:"n" type:"string"`       // reflected as number, overridden to string
	}
	fs := deriveFields(reflect.TypeOf(In{}))
	got := map[string]ParamType{}
	for _, f := range fs {
		got[f.Name] = f.Type
	}
	if got["chat_id"] != TNumber {
		t.Errorf("chat_id should be overridden to number by its type tag, got %s", got["chat_id"])
	}
	if got["raw"] != TJSON {
		t.Errorf("raw has no override and should be json, got %s", got["raw"])
	}
	if got["n"] != TString {
		t.Errorf("n should be overridden to string, got %s", got["n"])
	}
}

// The file-array contract: []*testFile and []File become array<file>, the one and only spelling for a
// file list (web docs/type-system.md §12). The variable picker recognises itemType=file and can bind
// the whole group to a multimodal image block or a file-list parameter, and the frontend expands the
// sub-fields as FileValue. It once reported a generic array with no ItemType, so an image array was not
// recognised as a file type and users had to detour through an array node; there was also a historical
// file+multiple form, abolished across the stack on 2026-08-05.
func TestDeriveFileSliceField(t *testing.T) {
	type Out struct {
		Images []*testFile `sokel:"images"`
		Single *testFile   `sokel:"voice"`
	}
	fs := deriveFields(reflect.TypeOf(Out{}))
	if fs[0].Type != TArray || fs[0].ItemType != TFile {
		t.Fatalf("images should be array<file>: %+v", fs[0])
	}
	if fs[0].Opaque {
		t.Errorf("array<file> has a definite element type and is not opaque: %+v", fs[0])
	}
	if len(fs[0].Fields) != 0 {
		t.Errorf("a file list carries no sub-fields; the frontend expands them as FileValue: %+v", fs[0].Fields)
	}
	if fs[1].Type != TFile || fs[1].ItemType != "" {
		t.Errorf("a single file should be file with no ItemType: %+v", fs[1])
	}
}

// Required semantics for files: a file parameter has to be a pointer (*testFile), since a value type
// would be taken for a json struct — but the "pointer implies optional" rule would then make it
// impossible to declare a file required. Files are the exception: they follow the explicit ,optional
// marker, required by default and optional only when marked.
func TestFileRequiredSemantics(t *testing.T) {
	type In struct {
		File   *testFile   `sokel:"file"`           // no optional, so required
		Cover  *testFile   `sokel:"cover,optional"` // explicitly optional
		Attach []*testFile `sokel:"attach"`         // several files, no optional, so required
		Note   *string     `sokel:"note"`           // a non-file pointer still follows the old pointer-implies-optional rule
	}
	fs := deriveFields(reflect.TypeOf(In{}))
	req := map[string]bool{}
	for _, f := range fs {
		req[f.Name] = f.Required
	}
	if !req["file"] {
		t.Error("file has no optional and should be required")
	}
	if req["cover"] {
		t.Error("cover is marked optional and should be optional")
	}
	if !req["attach"] {
		t.Error("attach has no optional and should be required")
	}
	if req["note"] {
		t.Error("a non-file pointer should keep the old pointer-implies-optional semantics")
	}
}

// toSnake and acronyms: a run of capitals counts as one word (ID->id, URL->url, ChatID->chat_id,
// HTTPCode->http_code).
func TestToSnakeAcronyms(t *testing.T) {
	cases := map[string]string{"ID": "id", "URL": "url", "ChatID": "chat_id", "HTTPCode": "http_code", "Name": "name", "MessageID": "message_id"}
	for in, want := range cases {
		if got := toSnake(in); got != want {
			t.Errorf("toSnake(%q)=%q want %q", in, got, want)
		}
	}
}

// A panicking handler must not take the process down: invoke recovers and turns it into an error frame,
// so the node goes red and the plugin keeps serving. Reproduced from a real plugin that dereferenced an
// optional *StartPage pointer without a nil check, taking the process down whenever it was omitted.

// A recursive, self-referential struct (a tree, such as Block{ Blocks []*Block }): contract derivation
// has to terminate rather than overflow the stack. Reproduced from a real plugin whose Block held
// []*Block children and crashed on infinite recursion while Register derived the contract at startup.
func TestDeriveRecursiveStruct(t *testing.T) {
	type Block struct {
		Text   string   `sokel:"text"`
		Blocks []*Block `sokel:"blocks"` // self-referential: a child block has its parent's type
	}
	type Out struct {
		Root Block `sokel:"root"`
	}
	fs := deriveFields(reflect.TypeOf(Out{})) // not crashing is the pass condition
	root := fs[0]
	if root.Type != TJSON {
		t.Fatalf("root should be json: %+v", root)
	}
	// root should hold text and blocks; blocks is where the cycle closes, so it is an array that goes no
	// deeper and carries no sub-fields.
	var blocks *Field
	for i := range root.Fields {
		if root.Fields[i].Name == "blocks" {
			blocks = &root.Fields[i]
		}
	}
	if blocks == nil || blocks.Type != TArray {
		t.Fatalf("blocks should be an array: %+v", root.Fields)
	}
	if len(blocks.Fields) != 0 {
		t.Errorf("an array at a cycle must not expand further, to avoid infinite recursion: %+v", blocks.Fields)
	}
}

// Cycle detection is per path rather than global, so the same type in sibling branches is not caught by
// mistake: both Bbox fields should expand in full.
func TestDeriveSiblingSameTypeNotDeduped(t *testing.T) {
	type Bbox struct {
		X float64 `sokel:"x"`
	}
	type In struct {
		A Bbox `sokel:"a"`
		B Bbox `sokel:"b"`
	}
	fs := deriveFields(reflect.TypeOf(In{}))
	for _, f := range fs {
		if len(f.Fields) != 1 || f.Fields[0].Name != "x" {
			t.Errorf("siblings of the same type should each expand in full: %s -> %+v", f.Name, f.Fields)
		}
	}
}

// Deriving structure from a map: map[string]any is opaque and produces no valueType, while map[string]T
// expands recursively into one. That makes the Go language level correspond naturally to the type
// system's typed/opaque split (docs/type-system.md §3), rather than demoting every map to a
// structureless json as it once did.
func TestDeriveMapValueType(t *testing.T) {
	type slot struct {
		Source string `sokel:"source" enum:"item,fixed,var"`
		Path   string `sokel:"path,optional"`
	}
	type in struct {
		Loose  map[string]any    `sokel:"loose"`   // opaque: the value has no structure to speak of
		Typed  map[string]slot   `sokel:"typed"`   // open-ended keys with a definite value type
		Scalar map[string]string `sokel:"scalar"`  // a scalar value counts too
		BadKey map[int]slot      `sokel:"bad_key"` // a non-string key cannot be a JSON object, so opaque
	}
	got := map[string]Field{}
	for _, f := range deriveFields(reflect.TypeOf(in{})) {
		got[f.Name] = f
	}

	if f := got["loose"]; f.Type != TJSON || f.ValueType != nil {
		t.Errorf("map[string]any should be json with no valueType, got type=%s valueType=%+v", f.Type, f.ValueType)
	}
	if f := got["bad_key"]; f.ValueType != nil {
		t.Errorf("a non-string key should produce no valueType, got %+v", f.ValueType)
	}
	if f := got["scalar"]; f.ValueType == nil || f.ValueType.Type != TString {
		t.Errorf("map[string]string should have valueType string, got %+v", f.ValueType)
	}

	vt := got["typed"].ValueType
	if vt == nil || vt.Type != TJSON {
		t.Fatalf("map[string]slot should have valueType json, got %+v", vt)
	}
	if len(vt.Fields) != 2 {
		t.Fatalf("valueType should expand recursively into 2 sub-fields, got %d: %+v", len(vt.Fields), vt.Fields)
	}
	// Tag semantics such as enum and optional apply inside the recursion too; it does not merely report a
	// type name.
	if vt.Fields[0].Type != TEnum || len(vt.Fields[0].Options) != 3 {
		t.Errorf("a valueType sub-field should keep its enum options, got %+v", vt.Fields[0])
	}
	if vt.Fields[1].Required {
		t.Errorf("optional should apply to a valueType sub-field, got %+v", vt.Fields[1])
	}
}

// Weak typing has to be **a visible decision** rather than the default path (docs/type-system.md §3). A
// bare map[string]any or any field is explicitly marked opaque in the contract, the UI labels it as
// unconstrained, and the platform skips structural validation on that basis — otherwise "no structure
// was declared" and "structure was declared and happens to be empty" are indistinguishable.
func TestDeriveOpaqueMarking(t *testing.T) {
	type slot struct {
		Path string `sokel:"path"`
	}
	type in struct {
		Loose    map[string]any  `sokel:"loose"`    // a bare map, so opaque
		Anything any             `sokel:"anything"` // a bare any, so opaque
		Typed    map[string]slot `sokel:"typed"`    // a typed map is not opaque; it has a valueType
		Obj      slot            `sokel:"obj"`      // a struct is not opaque; it has fields
		Name     string          `sokel:"name"`     // scalars have nothing to do with opaque
	}
	got := map[string]Field{}
	for _, f := range deriveFields(reflect.TypeOf(in{})) {
		got[f.Name] = f
	}
	if !got["loose"].Opaque {
		t.Errorf("map[string]any should be marked opaque: %+v", got["loose"])
	}
	if !got["anything"].Opaque {
		t.Errorf("a bare any should be marked opaque: %+v", got["anything"])
	}
	if got["typed"].Opaque {
		t.Errorf("a map with a valueType should not be marked opaque: %+v", got["typed"])
	}
	if got["obj"].Opaque {
		t.Errorf("a struct with fields should not be marked opaque: %+v", got["obj"])
	}
	if got["name"].Opaque {
		t.Errorf("a scalar field has nothing to do with opaque: %+v", got["name"])
	}
}

// Enum options support display names: voice ids such as xiaoyan and aisjiuxu are codes, and mean nothing
// to a user reading a dropdown. The syntax is `enum:"value=label"`, and mixing in the older form without
// labels (`enum:"a,b"`) has to keep working. The separator is = rather than :, because a colon inside an
// enum value (a URL, a time) is far more common than an equals sign. The labels here are deliberately
// non-ASCII: display names are exactly where non-ASCII shows up in practice.
func TestDeriveEnumWithLabels(t *testing.T) {
	type in struct {
		Voice string `sokel:"voice" enum:"xiaoyan=小燕（女声）,aisjiuxu=许久（男声）"`
		Order string `sokel:"order" enum:"asc,desc"`
		Mixed string `sokel:"mixed" enum:"a,b=乙"`
	}
	got := map[string]Field{}
	for _, f := range deriveFields(reflect.TypeOf(in{})) {
		got[f.Name] = f
	}

	v := got["voice"]
	if v.Type != TEnum || len(v.Options) != 2 {
		t.Fatalf("voice should be a 2-option enum: %+v", v)
	}
	if v.Options[0].Value != "xiaoyan" || v.Options[0].Label != "小燕（女声）" {
		t.Errorf("value and label should be separated: %+v", v.Options[0])
	}

	// The older form: with no label, Label stays empty and the frontend falls back to the value, rather
	// than the contract carrying a redundant copy
	o := got["order"]
	if len(o.Options) != 2 || o.Options[0].Value != "asc" || o.Options[0].Label != "" {
		t.Errorf("the older label-less form should still work: %+v", o.Options)
	}

	m := got["mixed"]
	if len(m.Options) != 2 || m.Options[0].Value != "a" || m.Options[1].Label != "乙" {
		t.Errorf("mixing the two forms should keep each in place: %+v", m.Options)
	}
}

// When an array's elements have no structure, the array itself is marked opaque too. Prompted by a
// report pipeline's `type tag = map[string]any` plus `[]tag` — which looks like a slice of structs and
// is really []map[string]any. The AST generator and runtime reflection must reach the same verdict, or
// behaviour changes quietly when you switch between them.
func TestDeriveArrayOpaque(t *testing.T) {
	type row struct {
		Name string `sokel:"name"`
	}
	type in struct {
		Rows  []row            `sokel:"rows"`
		Blobs []map[string]any `sokel:"blobs"`
		Anys  []any            `sokel:"anys"`
		Names []string         `sokel:"names"`
	}
	got := map[string]Field{}
	for _, f := range deriveFields(reflect.TypeOf(in{})) {
		got[f.Name] = f
	}
	if got["rows"].Opaque || len(got["rows"].Fields) != 1 {
		t.Errorf("an array whose elements have structure should not be opaque: %+v", got["rows"])
	}
	if !got["blobs"].Opaque {
		t.Errorf("[]map[string]any should be marked opaque: %+v", got["blobs"])
	}
	if !got["anys"].Opaque {
		t.Errorf("[]any should be marked opaque: %+v", got["anys"])
	}
	if got["names"].Opaque {
		t.Errorf("an array of scalars is not opaque: %+v", got["names"])
	}
}

// A map[string]any inside a struct must be able to say **why** it has no structure — otherwise only the
// builder's field.Object can give a reason, and the ones in type definitions become black holes nobody
// explains.
func TestOpaqueTagReason(t *testing.T) {
	type in struct {
		Meta  map[string]any `sokel:"meta" opaque:"the keys and value types depend on the metadata fields this library declares"`
		Plain map[string]any `sokel:"plain"` // no reason given: still opaque, but the generator warns
	}
	got := map[string]Field{}
	for _, f := range deriveFields(reflect.TypeOf(in{})) {
		got[f.Name] = f
	}
	if m := got["meta"]; !m.Opaque || m.Desc == "" {
		t.Errorf("an opaque tag should record its reason: %+v", m)
	}
	if p := got["plain"]; !p.Opaque || p.Desc != "" {
		t.Errorf("without a reason it is still opaque but Desc stays empty, which is what the generator warns on: %+v", p)
	}
}

// An explicit type tag rewrites the derived type, so the opaque verdict has to look at **the final**
// type. `any` with `type:"number,string"` is a scalar union — an id that may be a number or a string —
// not an absence of structure. The opaque audit tool caught this ordering bug on its very first run.
func TestTypeTagClearsOpaque(t *testing.T) {
	type in struct {
		ID   any `sokel:"id" type:"number,string"`
		Blob any `sokel:"blob"` // no type tag: genuinely structureless
	}
	got := map[string]Field{}
	for _, f := range deriveFields(reflect.TypeOf(in{})) {
		got[f.Name] = f
	}
	if got["id"].Opaque {
		t.Errorf("a scalar union should not be opaque: %+v", got["id"])
	}
	if got["id"].Type != TNumber || len(got["id"].Types) != 2 {
		t.Errorf("the type tag should take effect: %+v", got["id"])
	}
	if !got["blob"].Opaque {
		t.Errorf("a bare any is still opaque: %+v", got["blob"])
	}
}

// Input binding has to honour sokel tags **recursively**. The output side (structToVars) always did,
// while inputs looked only at the top level — so snake_case fields inside nested structs bound to
// nothing silently: Go's case-insensitive JSON matching does not cross an underscore, `doc_id` never
// reaches `DocID`, and no error is raised.
func TestBindInputNested(t *testing.T) {
	type chunk struct {
		ID       string `sokel:"id"`
		DocID    string `sokel:"doc_id"`
		ParentID string `sokel:"parent_id"`
	}
	type inner struct {
		HeapAllocBytes uint64 `sokel:"heap_alloc_bytes"`
	}
	type in struct {
		KbID   string           `sokel:"kb_id"`
		Chunks []chunk          `sokel:"chunks"`
		Mem    inner            `sokel:"mem"`
		ByName map[string]chunk `sokel:"by_name"`
		Ptr    *chunk           `sokel:"ptr"`
	}

	var got in
	raw := []byte(`{
		"kb_id":"kb1",
		"chunks":[{"id":"c1","doc_id":"d1","parent_id":"p1"}],
		"mem":{"heap_alloc_bytes":42},
		"by_name":{"a":{"id":"c2","doc_id":"d2"}},
		"ptr":{"id":"c3","doc_id":"d3"}
	}`)
	if err := BindInput(raw, &got); err != nil {
		t.Fatal(err)
	}
	if got.KbID != "kb1" {
		t.Errorf("top level: %+v", got)
	}
	if len(got.Chunks) != 1 || got.Chunks[0].DocID != "d1" || got.Chunks[0].ParentID != "p1" {
		t.Errorf("a snake_case field inside an array element should bind: %+v", got.Chunks)
	}
	if got.Mem.HeapAllocBytes != 42 {
		t.Errorf("nested struct: %+v", got.Mem)
	}
	if got.ByName["a"].DocID != "d2" {
		t.Errorf("nested inside a map value: %+v", got.ByName)
	}
	if got.Ptr == nil || got.Ptr.DocID != "d3" {
		t.Errorf("pointer field: %+v", got.Ptr)
	}
}

// Output and input must be inverses of one another: emitting a struct and binding it back must lose no
// fields.
func TestBindInputRoundTripsVars(t *testing.T) {
	type item struct {
		DocID string `sokel:"doc_id"`
		N     int    `sokel:"n"`
	}
	type payload struct {
		Items []item `sokel:"items"`
		Note  string `sokel:"note"`
	}
	src := payload{Items: []item{{DocID: "d1", N: 7}}, Note: "hi"}
	vars := StructToVars(src)
	raw, err := json.Marshal(vars)
	if err != nil {
		t.Fatal(err)
	}
	var back payload
	if err := BindInput(raw, &back); err != nil {
		t.Fatal(err)
	}
	if len(back.Items) != 1 || back.Items[0].DocID != "d1" || back.Items[0].N != 7 || back.Note != "hi" {
		t.Errorf("the round trip lost fields: %+v <- %s", back, raw)
	}
}

// testFile is a stub implementing FileRef. The contract package knows nothing of the platform file
// runtime — fetching bytes, uploading — and only needs to recognise "this is a file field", for which a
// marker suffices. That is precisely the decoupling point.
type testFile struct {
	ID   string `json:"id"`
	Name string `json:"name"`
}

func (*testFile) FileRef() {}

// An `opaque:"reason"` tag must **override** the automatic verdict.
//
// The automatic verdict recognises only a handful of shapes — a bare map, a bare any, structureless
// elements — and cannot see recursion: []*Block containing []*Block leaves the sub-fields empty through
// cycle detection, yet it is not structureless, merely inexpressible in the contract. This used to
// assign unconditionally, wiping out the tag along with the reason written in it — so the author had
// explained themselves and the audit kept reporting it anyway.
func TestOpaqueTagWinsOverInference(t *testing.T) {
	type Block struct {
		Text   string   `sokel:"text"`
		Blocks []*Block `sokel:"blocks" opaque:"blocks nest without limit; the contract cannot express a recursive structure"`
	}
	fs := DeriveFields(reflect.TypeOf(Block{}))
	var blocks *Field
	for i := range fs {
		if fs[i].Name == "blocks" {
			blocks = &fs[i]
		}
	}
	if blocks == nil {
		t.Fatal("the blocks field was not derived")
	}
	if !blocks.Opaque {
		t.Error("marked opaque should stay opaque and not be overwritten by the automatic verdict")
	}
	if blocks.Desc == "" {
		t.Error("the reason must survive in Desc, or the audit still does not know why")
	}
}

// An array of scalars records its ItemType ([]string -> string) rather than counting as structureless.
// Omitting it leaves downstream unable to tell an array of strings from a genuinely structureless
// []any.
func TestScalarArrayRecordsItemType(t *testing.T) {
	type X struct {
		Tags  []string         `sokel:"tags"`
		Sizes []int            `sokel:"sizes"`
		Blobs []map[string]any `sokel:"blobs"`
	}
	byName := map[string]Field{}
	for _, f := range DeriveFields(reflect.TypeOf(X{})) {
		byName[f.Name] = f
	}
	if byName["tags"].ItemType != TString || byName["tags"].Opaque {
		t.Errorf("[]string should record ItemType=string and not be opaque: %+v", byName["tags"])
	}
	if byName["sizes"].ItemType != TNumber {
		t.Errorf("[]int should record ItemType=number: %+v", byName["sizes"])
	}
	if byName["blobs"].ItemType != "" || !byName["blobs"].Opaque {
		t.Errorf("[]map[string]any genuinely has no structure, so opaque with no ItemType: %+v", byName["blobs"])
	}
}
