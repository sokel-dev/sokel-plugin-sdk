// Copyright 2026 The Sokel Authors
// SPDX-License-Identifier: Apache-2.0

package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"

	"github.com/sokel-dev/sokel-plugin-sdk/contract"
	"github.com/sokel-dev/sokel-plugin-sdk/plugin"
)

// The reference plugin, declared once in manifest.yml and generated into four languages, must report
// **one** contract. Python and TypeScript already assert that against contract.golden.json; this is
// the Go side of the same assertion, and the reason a Go plugin may now be declared in a manifest at
// all: without it, "Go from a manifest" would be a claim rather than a fact.
//
// The auth flow's reserved operations are excluded on purpose: the SDK contributes auth.start /
// auth.poll / auth.submit at handshake time from the declaration in zz_auth.go, so the generated code
// does not declare them and should not. They are compared against the same golden on the SDK side
// (sokel/auth_test.go TestAuthFlowOpsMatchKitchenSinkGolden), so the three languages assert the whole file.
func TestContractEqualsGolden(t *testing.T) {
	c := &collector{}
	OnChatStream(c, nil)
	OnEchoAll(c, nil)
	OnFileDigest(c, nil)
	OnHealthCheck(c, nil)
	OnRowstoreQuery(c, nil)
	OnVectorstoreQuery(c, nil)
	OnVectorstoreKeywordNgramKeywordQuery(c, nil)
	RegisterCredential(c)
	DeclareEvents(c)

	raw, err := os.ReadFile(filepath.Join("..", "contract.golden.json"))
	if err != nil {
		t.Fatal(err)
	}
	var golden struct {
		Operations   []contract.Operation `json:"operations"`
		Credential   []contract.Field     `json:"credential_schema"`
		Events       []contract.Event     `json:"events"`
		EventsCommon []contract.Field     `json:"events_common"`
	}
	if err := json.Unmarshal(raw, &golden); err != nil {
		t.Fatal(err)
	}
	var wantOps []contract.Operation
	for _, op := range golden.Operations {
		if !strings.HasPrefix(op.ID, "auth.") {
			wantOps = append(wantOps, op)
		}
	}

	sortOps(c.ops)
	sortOps(wantOps)
	eq(t, "operations", c.ops, wantOps)
	eq(t, "credential_schema", c.cred, golden.Credential)
	eq(t, "events", c.events, golden.Events)
	eq(t, "events_common", c.common, golden.EventsCommon)
}

func sortOps(ops []contract.Operation) {
	sort.Slice(ops, func(i, j int) bool { return ops[i].ID < ops[j].ID })
}

// eq compares through JSON rather than reflect.DeepEqual: the wire form is what the platform sees,
// and it is also what the other two languages are compared in.
func eq(t *testing.T, what string, got, want any) {
	t.Helper()
	g, _ := json.Marshal(got)
	w, _ := json.Marshal(want)
	if string(g) != string(w) {
		t.Errorf("%s differs from the golden contract\n got: %s\nwant: %s", what, g, w)
	}
}

// collector is a plugin.Host that records what the generated code declares, nothing more.
type collector struct {
	ops    []contract.Operation
	cred   []contract.Field
	events []contract.Event
	common []contract.Field
	names  []string
}

func (c *collector) Register(op contract.Operation, _ plugin.Invoke) { c.ops = append(c.ops, op) }
func (c *collector) SetCredentialContract(f []contract.Field)        { c.cred = f }
func (c *collector) DeclareEvent(e contract.Event)                   { c.events = append(c.events, e) }
func (c *collector) DeclareEventsCommon(f []contract.Field, names []string) {
	c.common, c.names = f, names
}
