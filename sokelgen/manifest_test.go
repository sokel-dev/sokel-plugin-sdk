// Copyright 2026 The Sokel Authors
// SPDX-License-Identifier: Apache-2.0

package sokelgen

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// YAML and JSON have to be one format. Implementing two parse routes separately would eventually produce
// a key that YAML supports and JSON does not, and nobody goes looking for that kind of difference.
func TestParseManifest_YAMLEqualsJSON(t *testing.T) {
	yamlSrc := `
plugin: { name: demo }
operations:
  - id: do_it
    label: Do it
    inputs:
      - { name: who, type: string, required: true }
      - { name: mode, type: enum, options: [fast, { value: full, label: Full }], default: fast }
    outputs:
      - { name: ok, type: boolean, required: true }
`
	jsonSrc := `{
  "plugin": {"name": "demo"},
  "operations": [{
    "id": "do_it", "label": "Do it",
    "inputs": [
      {"name": "who", "type": "string", "required": true},
      {"name": "mode", "type": "enum", "options": ["fast", {"value": "full", "label": "Full"}], "default": "fast"}
    ],
    "outputs": [{"name": "ok", "type": "boolean", "required": true}]
  }]
}`
	fromYAML, err := ParseManifest([]byte(yamlSrc), false)
	if err != nil {
		t.Fatalf("YAML: %v", err)
	}
	fromJSON, err := ParseManifest([]byte(jsonSrc), true)
	if err != nil {
		t.Fatalf("JSON: %v", err)
	}
	a, _ := json.Marshal(fromYAML)
	b, _ := json.Marshal(fromJSON)
	if string(a) != string(b) {
		t.Fatalf("the two forms parse differently:\nYAML: %s\nJSON: %s", a, b)
	}
	if got := fromYAML.Operations[0].Inputs[1].Options; len(got) != 2 || got[0].Value != "fast" || got[1].Label != "Full" {
		t.Fatalf("both enum option forms should be accepted: %+v", got)
	}
}

// A misspelled key has to fail on the spot. Dropping a field silently means the author believes it was
// declared while the platform has nothing, which is the classic failure of a declarative format.
func TestParseManifest_UnknownKeyIsError(t *testing.T) {
	_, err := ParseManifest([]byte("plugin: { name: demo }\noperations: [{id: a, inputs: [], outputs: []}]\nlable: typo\n"), false)
	if err == nil || !strings.Contains(err.Error(), "lable") {
		t.Fatalf("a misspelled top-level key was not rejected: %v", err)
	}
}

func TestManifest_Validate(t *testing.T) {
	cases := []struct {
		name string
		src  string
		want string
	}{
		{"uppercase operation id", "plugin: {name: d}\noperations: [{id: DoIt, inputs: [], outputs: []}]", "is invalid"},
		{"dotted operation id", "plugin: {name: d}\noperations: [{id: auth.start, inputs: [], outputs: []}]", "reserved namespace"},
		{"old auth convention", "plugin: {name: d}\noperations: [{id: auth_start, inputs: [], outputs: []}]", "credential.auth"},
		{"enum without options", "plugin: {name: d}\noperations: [{id: a, inputs: [{name: m, type: enum}], outputs: []}]", "no options"},
		{"unknown type", "plugin: {name: d}\noperations: [{id: a, inputs: [{name: m, type: strng}], outputs: []}]", "unknown type"},
		{"opaque without a reason", "plugin: {name: d}\noperations: [{id: a, inputs: [{name: m, type: json, opaque: true}], outputs: []}]", "requires a reason"},
		{"fields and valueType together", "plugin: {name: d}\noperations: [{id: a, inputs: [{name: m, type: json, fields: [{name: x, type: string}], valueType: {name: v, type: string}}], outputs: []}]", "mutually exclusive"},
		{"duplicate field name", "plugin: {name: d}\noperations: [{id: a, inputs: [{name: x, type: string},{name: x, type: string}], outputs: []}]", "duplicate field name"},
		{"goType reference with no definition", "plugin: {name: d}\noperations: [{id: a, inputs: [{name: m, type: json, goType: Ghost}], outputs: []}]", "nothing declares its fields"},
		{"common field missing from an event", "plugin: {name: d}\nevents_common: [chat_id]\nevents: [{id: a, fields: [{name: chat_id, type: string}]}, {id: b, fields: [{name: other, type: string}]}]\noperations: []", "does not exist in the contract"},
		{"common field with mismatched types", "plugin: {name: d}\nevents_common: [chat_id]\nevents: [{id: a, fields: [{name: chat_id, type: string}]}, {id: b, fields: [{name: chat_id, type: number}]}]\noperations: []", "different types across events"},
		{"common field hitting a reserved key", "plugin: {name: d}\nevents_common: [_event]\nevents: [{id: a, fields: [{name: _event, type: string}]}]\noperations: []", "platform-reserved key"},
		{"unknown auth kind", "plugin: {name: d}\ncredential: {auth: {kind: sms}}\noperations: [{id: a, inputs: [], outputs: []}]", "is invalid"},
		{"oauth without provider", "plugin: {name: d}\ncredential: {auth: {kind: oauth}}\noperations: [{id: a, inputs: [], outputs: []}]", "requires a provider"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := ParseManifest([]byte(tc.src), false)
			if err == nil {
				t.Fatalf("this declaration should have been rejected: %s", tc.src)
			}
			if !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("the error does not name the problem (should contain %q): %v", tc.want, err)
			}
		})
	}
}

// The sugar is only a spelling; what reaches the contract must be one of the protocol's own types.
func TestManifest_Sugar(t *testing.T) {
	m, err := ParseManifest([]byte(`
plugin: { name: d }
operations:
  - id: a
    inputs:
      - { name: n, type: int }
      - { name: fs, type: files }
      - { name: ss, type: strings }
    outputs: []
`), false)
	if err != nil {
		t.Fatal(err)
	}
	in := m.Operations[0].Inputs
	if in[0].Type != "number" || in[0].GoType != "int" {
		t.Errorf("the int sugar did not expand: %+v", in[0])
	}
	if in[1].Type != "array" || in[1].ItemType != "file" {
		t.Errorf("the files sugar did not expand: %+v", in[1])
	}
	if in[2].Type != "array" || in[2].ItemType != "string" {
		t.Errorf("the strings sugar did not expand: %+v", in[2])
	}
}

// Declare a structure once and reference it by name afterwards: an output echoing an input's structure
// is the common case, and a second transcription of it eventually drifts.
func TestManifest_GoTypeReference(t *testing.T) {
	m, err := ParseManifest([]byte(`
plugin: { name: d }
operations:
  - id: a
    inputs:
      - { name: p, type: json, goType: Profile, fields: [{name: nick, type: string}] }
    outputs:
      - { name: p, type: json, goType: Profile }
`), false)
	if err != nil {
		t.Fatal(err)
	}
	out := m.Operations[0].Outputs[0]
	if len(out.Fields) != 1 || out.Fields[0].Name != "nick" {
		t.Fatalf("the goType reference did not resolve to a structure: %+v", out)
	}
}

// Declaring an auth flow means the contract must hold its reserved operations: the platform panel builds
// requests from the contract, and without them it does not know which parameters to send.
func TestContractJSON_AuthOperations(t *testing.T) {
	m, err := ParseManifest([]byte(`
plugin: { name: d }
credential: { auth: { kind: qr } }
operations: [{ id: a, inputs: [], outputs: [] }]
`), false)
	if err != nil {
		t.Fatal(err)
	}
	cj, err := contractJSON(m, "")
	if err != nil {
		t.Fatal(err)
	}
	ids := map[string]bool{}
	for _, op := range cj["operations"].([]any) {
		ids[op.(map[string]any)["id"].(string)] = true
	}
	if !ids["auth.start"] || !ids["auth.poll"] {
		t.Fatalf("a qr auth flow's reserved operations did not reach the contract: %v", ids)
	}
	if ids["auth.submit"] {
		t.Fatal("qr should have no submit; the steps follow from the kind")
	}
	flow := cj["auth_flow"].(map[string]any)
	if flow["kind"] != "qr" {
		t.Fatalf("auth_flow was not reported: %v", flow)
	}
}

// Exporting YAML and reading it back must give the same contract. If the round trip loses anything, the
// route of taking a Go plugin's declaration as another language's starting point is broken.
func TestManifestYAML_RoundTrip(t *testing.T) {
	src := `
plugin: { name: demo, label: Demo }
credential:
  auth: { kind: input }
  fields: [{ name: api_key, type: secret, required: true }]
events_common: [chat_id]
events:
  - id: msg
    fields:
      - { name: chat_id, type: string, required: true }
      - { name: body, type: json, opaque: true, desc: passed through from upstream verbatim }
operations:
  - id: a
    stream: true
    timeoutSec: 30
    inputs:
      - { name: doc, type: json, oneOf: [{name: A, type: json, fields: [{name: t, type: string}]}] }
      - { name: kv, type: json, valueType: { name: v, type: number } }
    outputs: [{ name: ok, type: boolean, required: true }]
`
	m, err := ParseManifest([]byte(src), false)
	if err != nil {
		t.Fatal(err)
	}
	out, err := RenderManifestYAML(m)
	if err != nil {
		t.Fatal(err)
	}
	back, err := ParseManifest([]byte(out), false)
	if err != nil {
		t.Fatalf("the exported YAML cannot be read back; the round trip is broken: %v\n%s", err, out)
	}
	a, _ := ExportManifestJSON(m, "")
	b, _ := ExportManifestJSON(back, "")
	if string(a) != string(b) {
		t.Fatalf("the contract changed across the round trip:\n%s\n---\n%s", a, b)
	}
}

// The reference plugin's contract is the golden file: all three languages embed exactly it.
func TestKitchenSink_MatchesGolden(t *testing.T) {
	dir := filepath.Join("..", "examples", "kitchen-sink")
	// Resolve through FindManifest rather than a hardcoded name: the test then also covers the
	// real lookup, and a future rename touches ManifestNames only.
	path, err := FindManifest(dir)
	if err != nil || path == "" {
		t.Fatalf("no manifest in %s: %v", dir, err)
	}
	m, err := LoadManifest(path)
	if err != nil {
		t.Fatal(err)
	}
	doc, err := m.DocMarkdown()
	if err != nil {
		t.Fatal(err)
	}
	got, err := ExportManifestJSON(m, doc)
	if err != nil {
		t.Fatal(err)
	}
	want, err := os.ReadFile(filepath.Join(dir, "contract.golden.json"))
	if err != nil {
		t.Fatal(err)
	}
	if strings.TrimSpace(string(got)) != strings.TrimSpace(string(want)) {
		t.Fatalf("the reference plugin's contract disagrees with the golden file; changing manifest.yml means updating it:\nsokel-gen export json ./examples/kitchen-sink > examples/kitchen-sink/contract.golden.json")
	}
}
