// Copyright 2026 The Sokel Authors
// SPDX-License-Identifier: Apache-2.0

package sokelgen

import (
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// The Python renderer: the same IR becomes pydantic models. It is also the way to **check the IR is
// sufficient** — while existing plugins have not yet frozen it, a missing field can still be added.
func TestRenderPython(t *testing.T) {
	osInfo := []Field{{Name: "name", Type: "string"}, {Name: "arch", Type: "string"}}
	ops := []OpIO{{
		OpID: "file_digest", Label: "File digest",
		Inputs: []Field{
			{Name: "file", Type: "file", Required: true},
			{Name: "algo", Type: "enum", Default: "md5", Options: []Option{{Value: "md5"}, {Value: "sha256", Label: "SHA-256"}}},
			{Name: "tags", Type: "array", ItemType: "string"},
			{Name: "blob", Type: "json", Opaque: true},
			{Name: "doc", Type: "json", OneOf: []OneOfVariant{{Name: "A", Type: "json"}}},
		},
		Outputs: []Field{
			{Name: "sum", Type: "string", Required: true},
			{Name: "os", Type: "json", GoType: "OSInfo", Fields: osInfo},
			{Name: "hosts", Type: "array", GoType: "OSInfo", Fields: osInfo}, // the same named type is reused, not generated twice
		},
	}}

	src, err := RenderPython(ops)
	if err != nil {
		t.Fatalf("rendering failed: %v", err)
	}

	// The syntax has to be valid: generating Python by concatenation trips most easily on indentation
	dir := t.TempDir()
	f := filepath.Join(dir, "models.py")
	if err := os.WriteFile(f, []byte(src), 0o644); err != nil {
		t.Fatal(err)
	}
	if out, err := exec.Command("python3", "-c",
		"import sys; compile(open(sys.argv[1]).read(), sys.argv[1], 'exec')", f).CombinedOutput(); err != nil {
		t.Fatalf("the generated Python is not syntactically valid: %v\n%s\n---\n%s", err, out, src)
	}

	for _, want := range []string{
		`class OSInfo\(BaseModel\)`,
		`class FileDigestIn\(BaseModel\)`,
		`class FileDigestOut\(BaseModel\)`,
		`file: `,
		`algo: str = "md5"`,      // the default carries through
		`tags: list\[str\]`,      // scalar elements
		`blob: dict\[str, Any\]`, // opaque
		`doc: Any`,               // oneOf
		`os: OSInfo`,             // a named type reused; GoType's job here is to name the struct
		`hosts: list\[OSInfo\]`,  // array elements reuse it too
	} {
		if !regexp.MustCompile(want).MatchString(src) {
			t.Errorf("missing fragment %q\n---\n%s", want, src)
		}
	}
	// A given named type should be defined once
	if strings.Count(src, "class OSInfo(BaseModel)") != 1 {
		t.Errorf("OSInfo should be generated only once\n---\n%s", src)
	}
	// Required first, defaulted after, or Python raises a SyntaxError outright; the compile above already
	// covers it, and this states the intent
	if strings.Index(src, "sum: str") > strings.Index(src, "os: OSInfo") {
		t.Errorf("field order should follow the declaration\n---\n%s", src)
	}
}

// How a oneOf reads in Python: Python has real union types, so it should not fall back to Any. A union
// of array elements becomes list[A | B] and a field-level union becomes A | B, with both sets of
// branches generating models.
func TestPythonUnion(t *testing.T) {
	ops := []OpIO{{
		OpID: "chat", InType: "ChatIn", OutType: "ChatOut",
		Inputs: []Field{
			{Name: "parts", Type: "array", Required: true, OneOf: []OneOfVariant{
				{Name: "TextPart", GoType: "TextPart", Type: "json",
					Fields: []Field{{Name: "text", Type: "string", Required: true}}},
				{Name: "ImagePart", GoType: "ImagePart", Type: "json",
					Fields: []Field{{Name: "url", Type: "string", Required: true}}},
			}},
			{Name: "doc", Type: "json", Required: true, OneOf: []OneOfVariant{
				{Name: "TextPart", GoType: "TextPart", Type: "json",
					Fields: []Field{{Name: "text", Type: "string", Required: true}}},
			}},
		},
	}}
	src, err := RenderPython(ops)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(src, "parts: list[TextPart | ImagePart]") {
		t.Errorf("a union of array elements should be list[A | B]:\n%s", src)
	}
	if !strings.Contains(src, "doc: TextPart") {
		t.Errorf("a field-level union should be a union type:\n%s", src)
	}
	// The branch types need models of their own, defined once per name; both places reference TextPart
	if n := strings.Count(src, "class TextPart(BaseModel)"); n != 1 {
		t.Errorf("TextPart should be defined exactly once, got %d:\n%s", n, src)
	}
	if !strings.Contains(src, "class ImagePart(BaseModel)") {
		t.Errorf("the ImagePart model is missing:\n%s", src)
	}
}
