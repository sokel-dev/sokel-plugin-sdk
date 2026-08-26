// Copyright 2026 The Sokel Authors
// SPDX-License-Identifier: Apache-2.0

package sokelgen

import (
	"encoding/json"
	"strings"
	"testing"
)

// oneOf is the primary reason sokel-gen exists: reflection cannot map a type-name string back to a type,
// and Go has no union type, so a structural union **simply cannot be produced** by runtime reflection.
// The AST can: it locates the type named in the tag within the package and expands it
// (docs/plugin-sdk-multilang.md §1).
const oneofSrc = `package main

// DocObject is a neutral document object.
type DocObject struct {
	Title  string ` + "`sokel:\"title\"`" + `
	Blocks []Block ` + "`sokel:\"blocks\"`" + `
}

type Block struct {
	Text string ` + "`sokel:\"text\"`" + `
}

// BlocksArray is a bare array of blocks.
type BlocksArray []Block

type IngestIn struct {
	Doc any ` + "`sokel:\"document\" oneof:\"DocObject,BlocksArray\" label:\"Structured document\"`" + `
}
`

func TestParseOneOf(t *testing.T) {
	fields, err := ParseStructFields(oneofSrc, "IngestIn")
	if err != nil {
		t.Fatalf("parsing failed: %v", err)
	}
	if len(fields) != 1 {
		t.Fatalf("there should be 1 input: %+v", fields)
	}
	f := fields[0]
	dump, _ := json.Marshal(f)

	if len(f.OneOf) != 2 {
		t.Fatalf("the oneof should expand into 2 branches: %s", dump)
	}
	// Branch 1: a struct becomes json with its sub-fields expanded recursively
	v0 := f.OneOf[0]
	if v0.Name != "DocObject" || v0.Type != "json" {
		t.Errorf("branch 0 should be json: %s", dump)
	}
	if len(v0.Fields) != 2 {
		t.Errorf("branch 0 should expand title and blocks: %s", dump)
	}
	// A comment on the type doubles as the branch label, which only the AST route can see
	if v0.Label == "" {
		t.Errorf("a branch should carry a readable label: %s", dump)
	}
	// Branch 2: a named slice becomes an array with its element structure
	v1 := f.OneOf[1]
	if v1.Name != "BlocksArray" || v1.Type != "array" {
		t.Errorf("branch 1 should be an array: %s", dump)
	}
	if len(v1.Fields) != 1 || v1.Fields[0].Name != "text" {
		t.Errorf("branch 1 should expand its element structure: %s", dump)
	}
	// A field declaring a oneof is not itself opaque: it has structure, just more than one
	if f.Opaque {
		t.Errorf("a field with a oneof should not be marked opaque: %s", dump)
	}
}

// A oneof naming a type that does not exist fails **at generation time**. That is one of codegen's
// advantages over reflection: this class of error used to blow up on the platform side at the
// registration handshake, once the plugin had already started.
func TestParseOneOfUnknownType(t *testing.T) {
	src := `package main

type In struct {
	Doc any ` + "`sokel:\"document\" oneof:\"Ghost\"`" + `
}
`
	_, err := ParseStructFields(src, "In")
	if err == nil {
		t.Fatal("referencing a non-existent type should fail")
	}
	if !strings.Contains(err.Error(), "Ghost") {
		t.Errorf("the error should name the type: %v", err)
	}
}
