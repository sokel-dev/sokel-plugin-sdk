// Copyright 2026 The Sokel Authors
// SPDX-License-Identifier: Apache-2.0

package sokelgen

import (
	"strings"
	"testing"
)

// End to end: from a schema package's source, find the Schema types, generate and run a throwaway
// program, and get the complete declarations back. It uses a real package (internal/demoschema) rather
// than a string sample, because the builders are executable code and only actually running them shows
// whether chained calls, defaults and nested structures all come out right.
func TestLoadDeclarations(t *testing.T) {
	pkg, err := LoadDir("internal/demoschema")
	if err != nil {
		t.Fatalf("loading the package failed: %v", err)
	}
	types := pkg.SchemaTypes()
	if len(types) != 2 {
		t.Fatalf("2 Schemas should be found: %v", types)
	}

	ops, err := LoadDeclarations("internal/demoschema", "github.com/sokel-dev/sokel-plugin-sdk/sokelgen/internal/demoschema", types)
	if err != nil {
		t.Fatalf("reading the declarations failed: %v", err)
	}
	if len(ops) != 2 {
		t.Fatalf("2 operations should come back: %+v", ops)
	}

	byID := map[string]OpIO{}
	for _, o := range ops {
		byID[o.OpID] = o
	}

	fd := byID["file_digest"]
	if len(fd.Inputs) != 3 {
		t.Fatalf("file_digest should have 3 inputs: %+v", fd.Inputs)
	}
	if fd.Inputs[0].Type != "file" || !fd.Inputs[0].Required {
		t.Errorf("the file input, required by default: %+v", fd.Inputs[0])
	}
	// Both the enum labels and "a default implies optional" have to come through faithfully
	algo := fd.Inputs[1]
	if algo.Type != "enum" || len(algo.Options) != 2 || algo.Options[1].Label != "SHA-256" {
		t.Errorf("enum options: %+v", algo)
	}
	if algo.Required || algo.Default != "md5" {
		t.Errorf("a default should imply optional: %+v", algo)
	}
	// opaque has to reach the contract with its reason, so a reviewer can see why structure was given up
	// here
	extra := fd.Inputs[2]
	if !extra.Opaque || extra.Desc == "" {
		t.Errorf("opaque should record its reason: %+v", extra)
	}

	si := byID["system_info"]
	if len(si.Inputs) != 1 || si.Inputs[0].ItemType != "string" {
		t.Errorf("an array's scalar element type must be expressible: %+v", si.Inputs)
	}
	if len(si.Outputs) != 1 || len(si.Outputs[0].Fields) != 2 {
		t.Errorf("a nested struct should expand: %+v", si.Outputs)
	}
	// The type name has to survive the run-to-read-declarations layer: generating the Out struct reuses
	// OSInfo rather than rebuilding an equivalent from Fields, since converting between two such types is
	// exactly the runtime conversion this eliminates.
	if si.Outputs[0].GoType != "OSInfo" {
		t.Errorf("the Go type name should survive into the IR: %+v", si.Outputs[0])
	}

	// The generated type names pair with the OnXxx registration functions, and SchemaType lets that code
	// point back at the declaration itself
	if fd.InType != "FileDigestIn" || fd.OutType != "FileDigestOut" || fd.SchemaType != "FileDigest" {
		t.Errorf("type names: %+v", fd)
	}
}

// Generated names must have exactly one source: the operation id. In/Out once came from the schema type
// name while On/Emitter came from the operation id — indistinguishable while the two agree, but the
// moment they differ (type Stream with id "egress_stream") the generated code does not compile.
func TestOpTypeNamesFollowOpID(t *testing.T) {
	ops := []OpIO{{OpID: "egress_stream", SchemaType: "Stream", InType: "EgressStreamIn", OutType: "EgressStreamOut", Stream: true}}
	src, err := RenderRegister("main", SchemaRef{Import: "x/schema", Name: "schema"}, ops)
	if err != nil {
		t.Fatal(err)
	}
	// The registration function, the emitter and the input type must all point at the same set of names
	for _, want := range []string{
		"func OnEgressStream(", "type EgressStreamEmitter struct",
		"*EgressStreamIn", "*EgressStreamOut",
	} {
		if !strings.Contains(src, want) {
			t.Errorf("missing %q:\n%s", want, src)
		}
	}
	// The contract is still read from the schema type, which is where the declaration lives
	if !strings.Contains(src, "contract.OperationOf(&schema.Stream{})") {
		t.Errorf("the contract should come from the schema type:\n%s", src)
	}
}
