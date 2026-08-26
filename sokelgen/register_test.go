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

// Generating OnXxx joins the schema's Meta, the generated contract and the author's handler. The
// signature is fully concrete — type safety lives here, and the sokel library holds no generics.
func TestRenderRegister(t *testing.T) {
	ops := []OpIO{
		{OpID: "file_digest", SchemaType: "FileDigest", InType: "FileDigestIn", OutType: "FileDigestOut",
			Inputs: []Field{{Name: "file", Type: "file"}}},
		{OpID: "watch", SchemaType: "Watch", InType: "WatchIn", OutType: "WatchOut", Stream: true},
	}
	src, err := RenderRegister("main", SchemaRef{Import: "x/schema", Name: "schema"}, ops)
	if err != nil {
		t.Fatalf("rendering failed: %v", err)
	}
	if _, err := parser.ParseFile(token.NewFileSet(), "zz_register.go", src, 0); err != nil {
		t.Fatalf("the generated code is not syntactically valid: %v\n---\n%s", err, src)
	}

	for _, want := range []string{
		// Non-streaming: one return value, the most common shape
		`func OnFileDigest\(h plugin\.Host, fn func\(plugin\.Ctx, \*FileDigestIn\) \(\*FileDigestOut, error\)\)`,
		// Streaming: a typed emitter that may send many times
		`func OnWatch\(h plugin\.Host, fn func\(plugin\.Ctx, \*WatchIn, \*WatchEmitter\) error\)`,
		`type WatchEmitter struct`,
		`func \(e \*WatchEmitter\) Vars\(v \*WatchOut\)`, // a type-safe wrapper; the library's own takes any
		`h\.Register\(contract\.OperationOf\(&schema\.FileDigest\{}\)`,
		`contract\.BindInput`,
	} {
		if !regexp.MustCompile(want).MatchString(src) {
			t.Errorf("missing fragment %q\n---\n%s", want, src)
		}
	}
	// The contract comes from the schema declaration itself (OperationOf) rather than the fields being
	// copied into the generated code, which would diverge as soon as the schema changed without a
	// regeneration.
	if strings.Contains(src, `Name: "file"`) {
		t.Errorf("the contract should not be expanded again in the registration code\n---\n%s", src)
	}
}
