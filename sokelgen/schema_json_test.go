// Copyright 2026 The Sokel Authors
// SPDX-License-Identifier: Apache-2.0

package sokelgen

import (
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

// sokel.schema.json is **the format's second definition**; the first is this package's parser. Two
// definitions drift apart unless somebody watches, and this test is that somebody: add a field to the
// declaration without updating the schema and the editor does not recognise it — no error, no
// completion, nothing until sokel-gen runs — which is precisely the failure that having a schema was
// meant to eliminate.
func TestJSONSchemaMatchesParser(t *testing.T) {
	raw, err := os.ReadFile(filepath.Join("..", "docs", "sokel.schema.json"))
	if err != nil {
		t.Fatal(err)
	}
	var doc map[string]any
	if err := json.Unmarshal(raw, &doc); err != nil {
		t.Fatalf("sokel.schema.json is not valid JSON: %v", err)
	}
	defs, _ := doc["$defs"].(map[string]any)

	cases := []struct {
		def string // empty means the root object
		typ reflect.Type
	}{
		{"", reflect.TypeOf(Manifest{})},
		{"plugin", reflect.TypeOf(PluginDecl{})},
		{"credential", reflect.TypeOf(CredentialDecl{})},
		{"auth", reflect.TypeOf(AuthDecl{})},
		{"event", reflect.TypeOf(EventDecl{})},
		{"operation", reflect.TypeOf(OperationDecl{})},
		{"codegen", reflect.TypeOf(CodegenDecl{})},
		{"field", reflect.TypeOf(Field{})},
		{"variant", reflect.TypeOf(OneOfVariant{})},
	}
	for _, tc := range cases {
		node := doc
		if tc.def != "" {
			n, ok := defs[tc.def].(map[string]any)
			if !ok {
				t.Errorf("the schema has no $defs.%s", tc.def)
				continue
			}
			node = n
		}
		props, _ := node["properties"].(map[string]any)
		if props == nil {
			t.Errorf("$defs.%s has no properties", tc.def)
			continue
		}
		tags := jsonTags(tc.typ)

		// Every key the parser accepts has to be in the schema, or the editor marks a valid declaration as
		// wrong
		for name := range tags {
			if _, ok := props[name]; !ok {
				t.Errorf("%s: the parser accepts %q and the schema does not list it (a field was added without updating the schema)", where(tc.def), name)
			}
		}
		// Every key the schema lists has to be one the parser accepts, or a registered snake_case alias —
		// otherwise the editor completes a key that DisallowUnknownFields refuses on the spot
		for name := range props {
			if _, ok := tags[name]; ok {
				continue
			}
			if alias, ok := keyAliases[name]; ok {
				if _, ok := tags[alias]; ok {
					continue
				}
			}
			t.Errorf("%s: the schema lists %q and the parser does not accept it (a field was removed without updating the schema)", where(tc.def), name)
		}
	}

	// The two ways to write an enum option: a bare string, or {value,label}
	opt, _ := defs["option"].(map[string]any)
	branches, _ := opt["oneOf"].([]any)
	if len(branches) != 2 {
		t.Fatalf("$defs.option should offer two forms, string and object, got %d", len(branches))
	}
	objProps, _ := branches[1].(map[string]any)["properties"].(map[string]any)
	for name := range jsonTags(reflect.TypeOf(Option{})) {
		if _, ok := objProps[name]; !ok {
			t.Errorf("$defs.option is missing %q", name)
		}
	}
}

// The types have to match the parser's own table: one missing from the schema gets flagged red by the
// editor, and one too many has the editor endorsing a form that fails at run time.
func TestJSONSchemaFieldTypesMatchWireTypes(t *testing.T) {
	raw, _ := os.ReadFile(filepath.Join("..", "docs", "sokel.schema.json"))
	var doc map[string]any
	if err := json.Unmarshal(raw, &doc); err != nil {
		t.Fatal(err)
	}
	defs := doc["$defs"].(map[string]any)
	field := defs["field"].(map[string]any)["properties"].(map[string]any)
	listed := map[string]bool{}
	for _, v := range field["type"].(map[string]any)["enum"].([]any) {
		listed[v.(string)] = true
	}
	sugar := map[string]bool{"int": true, "files": true, "ints": true, "strings": true}
	for name := range wireTypes {
		if !listed[name] {
			t.Errorf("the schema's field.type is missing the protocol type %q", name)
		}
	}
	for name := range listed {
		if !wireTypes[name] && !sugar[name] {
			t.Errorf("the schema's field.type has an extra %q, which the parser does not accept", name)
		}
	}
}

func jsonTags(t reflect.Type) map[string]bool {
	out := map[string]bool{}
	for i := 0; i < t.NumField(); i++ {
		f := t.Field(i)
		if !f.IsExported() {
			continue
		}
		name := strings.Split(f.Tag.Get("json"), ",")[0]
		if name == "" || name == "-" {
			continue
		}
		out[name] = true
	}
	return out
}

func where(def string) string {
	if def == "" {
		return "the root object"
	}
	return "$defs." + def
}
