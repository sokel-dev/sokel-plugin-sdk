// Copyright 2026 The Sokel Authors
// SPDX-License-Identifier: Apache-2.0

package sokelgen

import (
	"go/parser"
	"go/token"
	"regexp"
	"strings"
	"testing"
)

// Reverse migration: an old struct+tag contract becomes new schema declaration code. What comes out is
// **a starting point** — wherever a machine cannot decide, it leaves an explicit note for a human rather
// than guessing something in.
func TestRenderSchema(t *testing.T) {
	ops := []OpIO{{
		OpID: "send", Label: "Send", Desc: "send one message",
		Inputs: []Field{
			{Name: "chat_id", Type: "string", Required: true, Label: "Chat ID"},
			{Name: "mode", Type: "enum", Options: []Option{{Value: "HTML"}, {Value: "MD", Label: "Markdown"}}},
			{Name: "count", Type: "number", Default: float64(3)},
			{Name: "files", Type: "array", ItemType: "file"},
			{Name: "tags", Type: "array", ItemType: "string"},
			{Name: "rows", Type: "array", GoType: "Row", Fields: []Field{{Name: "x", Type: "string"}}},
			{Name: "doc", Type: "json", GoType: "DocObj", Fields: []Field{{Name: "t", Type: "string"}}},
			{Name: "blob", Type: "json"}, // structureless json in the old contract
		},
		Outputs: []Field{{Name: "ok", Type: "boolean", Required: true}},
	}}

	src, err := RenderSchema("schema", ops)
	if err != nil {
		t.Fatalf("rendering failed: %v", err)
	}
	if _, err := parser.ParseFile(token.NewFileSet(), "schema.go", src, 0); err != nil {
		t.Fatalf("the generated code is not syntactically valid: %v\n---\n%s", err, src)
	}

	for _, want := range []string{
		`type Send struct\{\}`,
		`func \(Send\) Meta\(\) sokel\.Meta`,
		`ID: "send", Label: "Send", Desc: "send one message"`,
		`field\.String\("chat_id"\)\.Label\("Chat ID"\)`,
		`field\.Enum\("mode", field\.Opt\("HTML"\), field\.Opt\("MD", "Markdown"\)\)`,
		`field\.Number\("count"\)\.Default\(3\)`,
		`field\.Files\("files"\)`,
		`field\.Array\("tags", \[\]string\{\}\)`,
		`field\.Array\("rows", \[\]Row\{\}\)`, // reference the plugin's existing type rather than rebuild it
		`field\.Json\("doc", DocObj\{\}\)`,
		`field\.Object\("blob", "reason pending"\)`, // forces a decision: declare the structure, or write down why there is none
	} {
		if !regexp.MustCompile(want).MatchString(src) {
			t.Errorf("missing fragment %q\n---\n%s", want, src)
		}
	}

	// Required by default and optional only when explicit, matching the builders' semantics
	if !strings.Contains(src, `field.Enum("mode", field.Opt("HTML"), field.Opt("MD", "Markdown")).Optional()`) {
		t.Errorf("a non-required field should say .Optional() explicitly\n---\n%s", src)
	}
	// A field with a default does not also get .Optional(); the default already implies it
	if strings.Contains(src, `.Default(3).Optional()`) {
		t.Errorf("a field with a default should not also carry .Optional()\n---\n%s", src)
	}
	// The manual checklist plus the types that still have to move
	if !strings.Contains(src, "a starting point for the") || !strings.Contains(src, "to move: DocObj, Row") {
		t.Errorf("the migration header should carry the checklist and the types to move\n---\n%s", src)
	}
}
