// Copyright 2026 The Sokel Authors
// SPDX-License-Identifier: Apache-2.0

package sokelgen

import (
	"strings"
	"testing"
)

// In the new form the generator looks not for sokel.Register calls but for **types implementing
// sokel.Schema**, with all three of Meta, Inputs and Outputs. The test is the method set rather than an
// explicit interface assertion: nobody writes `var _ sokel.Schema = FileDigest{}` of their own accord,
// and requiring it would add a ceremony for nothing.
const schemaSrc = `package schema

import (
	"github.com/sokel-dev/sokel-plugin-sdk/sokel"
	"github.com/sokel-dev/sokel-plugin-sdk/sokel/field"
)

// FileDigest digests a file.
type FileDigest struct{}

func (FileDigest) Meta() sokel.Meta { return sokel.Meta{ID: "file_digest"} }
func (FileDigest) Inputs() []sokel.FieldSpec { return []sokel.FieldSpec{field.File("file")} }
func (FileDigest) Outputs() []sokel.FieldSpec { return []sokel.FieldSpec{field.String("md5")} }

// SysInfo shows that a pointer receiver counts too.
type SysInfo struct{}

func (s *SysInfo) Meta() sokel.Meta { return sokel.Meta{ID: "system_info"} }
func (s *SysInfo) Inputs() []sokel.FieldSpec { return nil }
func (s *SysInfo) Outputs() []sokel.FieldSpec { return nil }

// Helper has only some of the methods, so it is not a Schema and must not be mistaken for one.
type Helper struct{}

func (Helper) Meta() sokel.Meta { return sokel.Meta{} }

// An ordinary type
type Row struct{ Name string }
`

func TestFindSchemas(t *testing.T) {
	pkg, err := Load(schemaSrc)
	if err != nil {
		t.Fatalf("loading failed: %v", err)
	}
	got := pkg.SchemaTypes()
	if len(got) != 2 {
		t.Fatalf("2 Schema types should be found, got %v", got)
	}
	// Determinism: sorted by name, or the generated output would come out in a different order each time
	if got[0] != "FileDigest" || got[1] != "SysInfo" {
		t.Errorf("should be sorted by name, with value and pointer receivers both counting: %v", got)
	}
	for _, bad := range []string{"Helper", "Row"} {
		for _, g := range got {
			if g == bad {
				t.Errorf("%s should not be taken for a Schema", bad)
			}
		}
	}
}

// A schema package with no Schema type at all fails, rather than producing a confusing empty file.
func TestFindSchemasEmpty(t *testing.T) {
	pkg, _ := Load(`package schema

type Row struct{ Name string }
`)
	if _, err := pkg.SchemaOps(); err == nil || !strings.Contains(err.Error(), "Schema") {
		t.Errorf("no Schema type should give a readable error: %v", err)
	}
}
