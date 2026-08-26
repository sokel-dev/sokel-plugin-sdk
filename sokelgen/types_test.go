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

// Generating the In/Out structs: contract fields become Go fields. The first floor is still valid syntax,
// which is where string concatenation trips most easily, and only then whether the type mapping is
// right.
func TestRenderTypes(t *testing.T) {
	ops := []OpIO{{
		OpID: "file_digest", InType: "FileDigestIn", OutType: "FileDigestOut",
		Inputs: []Field{
			{Name: "file", Type: "file", Required: true},
			{Name: "files", Type: "array", ItemType: "file"},
			{Name: "algo", Type: "enum", Options: []Option{{Value: "md5"}}},
			{Name: "count", Type: "number"},
			{Name: "on", Type: "boolean"},
			{Name: "hosts", Type: "array", ItemType: "string"},
			{Name: "os", Type: "json", GoType: "OSInfo"},                                  // reuse the declared type
			{Name: "rows", Type: "array", GoType: "Row"},                                  // elements reuse it too
			{Name: "blob", Type: "json", Opaque: true},                                    // structureless, so any
			{Name: "doc", Type: "json", OneOf: []OneOfVariant{{Name: "A", Type: "json"}}}, // a union, so a wrapper type
			{Name: "inline", Type: "json", Fields: []Field{{Name: "x", Type: "string"}}},  // anonymous, so inline
		},
		Outputs: []Field{{Name: "md5", Type: "string"}},
	}}

	src, err := RenderTypes("main", SchemaRef{Import: "x/schema", Name: "schema"}, ops)
	if err != nil {
		t.Fatalf("rendering failed: %v", err)
	}
	if _, err := parser.ParseFile(token.NewFileSet(), "zz_types.go", src, 0); err != nil {
		t.Fatalf("the generated code is not syntactically valid: %v\n---\n%s", err, src)
	}

	// Regexes rather than literal fragments: gofmt **aligns struct fields in columns**, so pinning the exact
	// number of spaces makes a brittle assertion (a trap I have fallen into twice). This pins only the
	// field-name-to-type correspondence.
	for _, want := range []string{
		`type FileDigestIn struct`,
		`type FileDigestOut struct`,
		`File\s+\*plugin\.File\s+.json:"file"`,
		`Files\s+\[\]\*plugin\.File`,
		`Algo\s+string`, // an enum is just a string
		`Count\s+float64`,
		`On\s+bool`,
		`Hosts\s+\[\]string`,      // scalar elements
		`OS\s+schema\.OSInfo`,     // reuse the declared type; os is an acronym, so the field is all caps
		`Rows\s+\[\]schema\.Row`,  // elements reuse it too
		`Blob\s+map\[string\]any`, // opaque
		`Doc\s+FileDigestInDoc`,   // a oneOf becomes a wrapper type (see union_test.go; any is too easy to assert wrongly)
	} {
		if !regexp.MustCompile(want).MatchString(src) {
			t.Errorf("missing fragment %q\n---\n%s", want, src)
		}
	}
	// An anonymous struct expands inline
	if !strings.Contains(src, "Inline struct {") {
		t.Errorf("anonymous json should expand inline\n---\n%s", src)
	}
	// snake_case becomes exported CamelCase, and the json tag keeps the contract's own name
	if strings.Contains(src, "file_digest struct") {
		t.Errorf("a type name should not be snake_case\n---\n%s", src)
	}
}

// With no schema package type in use, no unused import should be forced in; Go would fail to compile.
func TestRenderTypesNoUnusedImport(t *testing.T) {
	src, err := RenderTypes("main", SchemaRef{Import: "x/schema", Name: "schema"}, []OpIO{{
		OpID: "ping", InType: "PingIn", OutType: "PingOut",
		Inputs:  []Field{{Name: "msg", Type: "string"}},
		Outputs: []Field{{Name: "ok", Type: "boolean"}},
	}})
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(src, `"schema"`) {
		t.Errorf("an unused schema package should not be imported\n---\n%s", src)
	}
	if strings.Contains(src, "sokel.File") || strings.Contains(src, `go-sdk/sokel"`) {
		t.Errorf("with no file field the SDK should not be imported\n---\n%s", src)
	}
}

// Generated field names follow Go convention: acronyms in full caps. Without it they come out as KbId,
// Ok and Md5, which golint complains about and which nobody writes by hand.
func TestExportNameInitialisms(t *testing.T) {
	cases := map[string]string{
		"kb_id":     "KbID",
		"ids":       "IDs", // the plural is an acronym too
		"ok":        "OK",
		"md5":       "MD5",
		"doc_id":    "DocID",
		"http_url":  "HTTPURL",
		"num_cpu":   "NumCPU",
		"os":        "OS",
		"page_no":   "PageNo", // anything not in the table is unchanged
		"include_x": "IncludeX",
	}
	for in, want := range cases {
		if got := exportName(in); got != want {
			t.Errorf("exportName(%q) = %q, want %q", in, got, want)
		}
	}
}

// A union of array elements: type=array with oneOf. It generates []Wrapper rather than falling back to
// []any.
func TestRenderTypesArrayOfUnion(t *testing.T) {
	ops := []OpIO{{
		OpID: "chat", SchemaType: "Chat", InType: "ChatIn", OutType: "ChatOut",
		Inputs: []Field{{
			Name: "parts", Type: "array",
			OneOf: []OneOfVariant{
				{Name: "TextPart", GoType: "TextPart", Type: "json",
					Fields: []Field{{Name: "text", Type: "string"}}},
				{Name: "ImagePart", GoType: "ImagePart", Type: "json",
					Fields: []Field{{Name: "url", Type: "string"}}},
			},
		}},
	}}
	src, err := RenderTypes("gen", SchemaRef{Import: "x/schema", Name: "schema"}, ops)
	if err != nil {
		t.Fatal(err)
	}
	// The field is a slice of the wrapper type
	if !regexp.MustCompile(`Parts\s+\[\]ChatInParts\s+`).MatchString(src) {
		t.Errorf("the field should be []ChatInParts:\n%s", src)
	}
	// The wrapper type is defined once and carries two typed accessors
	if strings.Count(src, "type ChatInParts struct") != 1 {
		t.Errorf("the wrapper type should be defined exactly once:\n%s", src)
	}
	for _, m := range []string{"AsTextPart", "AsImagePart", "Raw"} {
		if !strings.Contains(src, ") "+m+"(") {
			t.Errorf("the %s accessor is missing:\n%s", m, src)
		}
	}
	if strings.Contains(src, "[]any") {
		t.Errorf("it should not fall back to []any:\n%s", src)
	}
}

// Generated fields must carry a **sokel** tag holding the contract name, not just a json tag. BindInput
// reads the sokel tag; without it, it falls back to "Go field name lowered to snake_case", which fails
// to bind whenever the contract name is not snake_case — p_http's responseType would miss ResponseType,
// file mode would silently degrade to text mode, and the result would be an empty file.
func TestRenderTypesEmitsSokelTag(t *testing.T) {
	ops := []OpIO{{OpID: "request", SchemaType: "Request", InType: "RequestIn", OutType: "RequestOut",
		Inputs: []Field{{Name: "responseType", Type: "string"}, {Name: "bodyType", Type: "string"}}}}
	src, err := RenderTypes("main", SchemaRef{Import: "x/schema", Name: "schema"}, ops)
	if err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"responseType", "bodyType"} {
		if !regexp.MustCompile(`json:"` + name + `" sokel:"` + name + `"`).MatchString(src) {
			t.Errorf("field %s should carry both a json and a sokel tag:\n%s", name, src)
		}
	}
}
