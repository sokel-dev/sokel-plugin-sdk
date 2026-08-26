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

// A oneOf gets a wrapper type rather than any, because any means assertions such as
// doc.(map[string]any) in the handler, which either panic or fail silently when written wrongly.
//
// The shape is raw bytes plus typed accessors rather than "one pointer per branch": the latter would
// have to decide the branch at unmarshal time, and a oneOf's runtime value carries **no discriminator**
// by protocol, so guessing from the structure guesses wrong. The handler tries branches in its own
// order, each attempt landing on a definite type.
func TestRenderTypesOneOfWrapper(t *testing.T) {
	ops := []OpIO{{
		OpID: "ingest", InType: "IngestIn", OutType: "IngestOut",
		Inputs: []Field{{
			Name: "doc", Type: "json",
			OneOf: []OneOfVariant{
				{Name: "DocObject", Type: "json", Fields: []Field{{Name: "title", Type: "string"}}},
				{Name: "Blocks", Type: "array", Fields: []Field{{Name: "text", Type: "string"}}},
			},
		}},
		Outputs: []Field{{Name: "ok", Type: "boolean"}},
	}}

	src, err := RenderTypes("main", SchemaRef{Import: "x/schema", Name: "schema"}, ops)
	if err != nil {
		t.Fatalf("rendering failed: %v", err)
	}
	if _, err := parser.ParseFile(token.NewFileSet(), "zz_types.go", src, 0); err != nil {
		t.Fatalf("the generated code is not syntactically valid: %v\n---\n%s", err, src)
	}

	for _, want := range []string{
		`Doc\s+IngestInDoc`, // the field uses the wrapper type, not any
		`type IngestInDoc struct`,
		`func \(u \*IngestInDoc\) UnmarshalJSON`, // keep the raw bytes; do not decide here
		`func \(u IngestInDoc\) AsDocObject\(\) \(\*schema\.DocObject, bool\)`,
		`func \(u IngestInDoc\) AsBlocks\(\) \(\[\]schema\.Blocks, bool\)`, // the array branch
		`func \(u IngestInDoc\) Raw\(\) json\.RawMessage`,                  // the escape hatch: cope on your own when nothing matches
		`json\.Unmarshal`,
	} {
		if !regexp.MustCompile(want).MatchString(src) {
			t.Errorf("missing fragment %q\n---\n%s", want, src)
		}
	}
	// It must not fall back to any
	if regexp.MustCompile(`Doc\s+any`).MatchString(src) {
		t.Errorf("a oneOf should not be any\n---\n%s", src)
	}
	// opaque is map[string]any: an object with open-ended keys, which is not the same as a oneOf, whose
	// shapes may differ entirely
	src2, _ := RenderTypes("main", SchemaRef{Import: "x/schema", Name: "schema"}, []OpIO{{
		OpID: "o", InType: "OIn", OutType: "OOut",
		Inputs: []Field{{Name: "blob", Type: "json", Opaque: true}},
	}})
	if !strings.Contains(src2, "Blob map[string]any") {
		t.Errorf("opaque should be map[string]any, an object with open-ended keys\n---\n%s", src2)
	}
}
