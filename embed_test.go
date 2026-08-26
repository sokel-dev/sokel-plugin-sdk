// Copyright 2026 The Sokel Authors
// SPDX-License-Identifier: Apache-2.0

package sdk

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/sokel-dev/sokel-plugin-sdk/sokelgen"
)

// The reference declaration compiled into the binary has to **work**: whatever `sokel-gen example`
// prints must parse when copied. Printing a declaration the tool cannot read itself is worse than
// printing none — whoever copies it, person or AI, will assume they made the mistake.
func TestExampleManifestParses(t *testing.T) {
	m, err := sokelgen.ParseManifest([]byte(ExampleManifest), false)
	if err != nil {
		t.Fatalf("the embedded reference declaration failed to parse: %v", err)
	}
	if len(m.Operations) == 0 || len(m.Events) == 0 || m.Credential == nil {
		t.Fatalf("the reference declaration should cover operations, events and a credential, got ops=%d events=%d cred=%v",
			len(m.Operations), len(m.Events), m.Credential != nil)
	}
}

func TestEmbeddedDocsAreUsable(t *testing.T) {
	if !strings.Contains(ManifestDoc, "## Field") {
		t.Error("the embedded guide has no Field section, so the wrong file was most likely embedded")
	}
	var doc map[string]any
	if err := json.Unmarshal([]byte(Schema), &doc); err != nil {
		t.Fatalf("the embedded schema is not valid JSON: %v", err)
	}
	if doc["$id"] == nil {
		t.Error("the schema has no $id")
	}
	for name, src := range map[string]string{"python": ExamplePython, "node": ExampleNode} {
		if !strings.Contains(src, "kitchen") {
			t.Errorf("the embedded %s implementation looks wrong; it does not mention kitchen-sink", name)
		}
	}
}
