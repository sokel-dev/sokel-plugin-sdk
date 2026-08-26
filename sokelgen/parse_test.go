// Copyright 2026 The Sokel Authors
// SPDX-License-Identifier: Apache-2.0

package sokelgen

import (
	"encoding/json"
	"testing"
)

// sokel-gen's core: derive the contract from **source** (the AST) rather than by runtime reflection. The
// first floor is equivalence with the reflection version — tag semantics, type mapping and the required
// verdict have to match word for word, or switching to codegen is a silent change of behaviour
// (docs/plugin-sdk-multilang.md §1).
const src = `package main

type slot struct {
	Path string ` + "`sokel:\"path\"`" + `
}

// SendMessageIn is the input of send_message.
type SendMessageIn struct {
	// The target chat id.
	ChatID string ` + "`sokel:\"chat_id\" label:\"Chat ID\"`" + `
	Text   string ` + "`sokel:\"text\" desc:\"The body text\"`" + `
	Parse  string ` + "`sokel:\"parse_mode,optional\" enum:\"HTML,Markdown\"`" + `
	Count  int    ` + "`sokel:\"count\" default:\"3\"`" + `
	Silent bool   ` + "`sokel:\"silent,optional\"`" + `
	Slots  map[string]slot ` + "`sokel:\"slots\"`" + `
	Loose  map[string]any  ` + "`sokel:\"loose\"`" + `
	Nested slot            ` + "`sokel:\"nested\"`" + `
	Skip   string          ` + "`sokel:\"-\"`" + `
	hidden string
}
`

func TestParseStructFields(t *testing.T) {
	fields, err := ParseStructFields(src, "SendMessageIn")
	if err != nil {
		t.Fatalf("parsing failed: %v", err)
	}
	idx := map[string]Field{}
	for _, f := range fields {
		idx[f.Name] = f
	}
	dump, _ := json.Marshal(fields)

	// The sokel tag's external name plus its label
	if f := idx["chat_id"]; f.Label != "Chat ID" || f.Type != "string" {
		t.Errorf("chat_id should take its sokel name and label: %s", dump)
	}
	// Unexported fields and sokel:"-" never appear
	if _, ok := idx["hidden"]; ok {
		t.Errorf("an unexported field should not reach the contract: %s", dump)
	}
	if _, ok := idx["skip"]; ok {
		t.Errorf("sokel:\"-\" should be skipped: %s", dump)
	}
	// The enum tag changes the type to enum and adds the options
	if f := idx["parse_mode"]; f.Type != "enum" || len(f.Options) != 2 || f.Options[0].Value != "HTML" {
		t.Errorf("an enum tag should produce options: %s", dump)
	}
	// The required verdict follows the same rule as reflection: not optional, no default, not a pointer
	if !idx["chat_id"].Required {
		t.Errorf("chat_id should be required: %s", dump)
	}
	if idx["parse_mode"].Required {
		t.Errorf("optional should not be required: %s", dump)
	}
	if idx["count"].Required {
		t.Errorf("a default should not be required: %s", dump)
	}
	// Type mapping
	if idx["count"].Type != "number" || idx["silent"].Type != "boolean" {
		t.Errorf("scalar type mapping is wrong: %s", dump)
	}
	// map[string]T recurses into a valueType; map[string]any is opaque
	if vt := idx["slots"].ValueType; vt == nil || len(vt.Fields) != 1 || vt.Fields[0].Name != "path" {
		t.Errorf("map[string]T should expand into a valueType: %s", dump)
	}
	if !idx["loose"].Opaque {
		t.Errorf("map[string]any should be marked opaque: %s", dump)
	}
	// A struct expands recursively into fields
	if len(idx["nested"].Fields) != 1 {
		t.Errorf("a struct should expand recursively: %s", dump)
	}

	// —— unique to the AST: the comment above a field becomes its desc, which reflection can never see ——
	if idx["chat_id"].Desc != "The target chat id." {
		t.Errorf("a field comment should become its desc: %q", idx["chat_id"].Desc)
	}
	// An explicit desc tag wins over the comment
	if idx["text"].Desc != "The body text" {
		t.Errorf("the desc tag should win: %q", idx["text"].Desc)
	}
}

// When an array's elements have no structure, the array itself is marked opaque too — otherwise
// []map[string]any and "[]SomeStruct that happens to export no fields" are indistinguishable in the
// contract, and the platform cannot tell whether to validate structure. Prompted by a report pipeline's
// `type tag = map[string]any` plus `[]tag`.
func TestArrayOpaqueFromElement(t *testing.T) {
	src := `package main

type row struct {
	Name string ` + "`sokel:\"name\"`" + `
}
type loose = map[string]any

type In struct {
	Rows   []row    ` + "`sokel:\"rows\"`" + `   // elements have structure, so not opaque
	Blobs  []loose  ` + "`sokel:\"blobs\"`" + `  // elements are a bare map, so opaque
	Anys   []any    ` + "`sokel:\"anys\"`" + `   // elements are any, so opaque
	Names  []string ` + "`sokel:\"names\"`" + `  // scalar elements: the type is definite and must not be taken for opaque
}
`
	fields, err := ParseStructFields(src, "In")
	if err != nil {
		t.Fatalf("解析失败: %v", err)
	}
	idx := map[string]Field{}
	for _, f := range fields {
		idx[f.Name] = f
	}
	if idx["rows"].Opaque || len(idx["rows"].Fields) != 1 {
		t.Errorf("an array whose elements have structure should not be opaque: %+v", idx["rows"])
	}
	if !idx["blobs"].Opaque {
		t.Errorf("[]map[string]any should be marked opaque: %+v", idx["blobs"])
	}
	if !idx["anys"].Opaque {
		t.Errorf("[]any should be marked opaque: %+v", idx["anys"])
	}
	if idx["names"].Opaque {
		t.Errorf("an array of scalars is not opaque; the element type is definite: %+v", idx["names"])
	}
}

// Integer recognition has to agree across both routes: the reflection side recorded the width long ago
// and the AST side, used for reverse migration, did not — so an old `ContentID int` migrated across as a
// float64, adding a conversion to the implementation out of nowhere.
func TestParseIntKind(t *testing.T) {
	src := `package main

type In struct {
	N     int     ` + "`sokel:\"n\"`" + `
	Big   int64   ` + "`sokel:\"big\"`" + `
	Ratio float64 ` + "`sokel:\"ratio\"`" + `
	Sizes []int   ` + "`sokel:\"sizes\"`" + `
}
`
	idx := map[string]Field{}
	fs, err := ParseStructFields(src, "In")
	if err != nil {
		t.Fatal(err)
	}
	for _, f := range fs {
		idx[f.Name] = f
	}
	if idx["n"].GoType != "int" {
		t.Errorf("an int field should remember it is an integer: %+v", idx["n"])
	}
	if idx["big"].GoType != "int64" {
		t.Errorf("the width must be kept; narrowing to int would lose precision: %+v", idx["big"])
	}
	if idx["ratio"].GoType != "" {
		t.Errorf("a float should carry no integer hint: %+v", idx["ratio"])
	}
	if idx["sizes"].GoType != "int" {
		t.Errorf("an integer array's element type: %+v", idx["sizes"])
	}
}
