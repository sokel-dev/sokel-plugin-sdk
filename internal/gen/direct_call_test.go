// Copyright 2026 The Sokel Authors
// SPDX-License-Identifier: Apache-2.0

package gen

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"
)

// A direct-call (http) plugin declares where inputs go (in), where outputs come from (from) and how its credential
// is put on the request (credential.inject); the manifest check catches the mistakes that would otherwise only show
// on the first real call.
func TestDirectCallDeclarations(t *testing.T) {
	const good = `
plugin: { org: acme, name: tickets, label: Tickets, version: 1.0.0 }
transports: [{ kind: http }]
credential:
  fields: [{ name: token, label: Token, type: secret, required: true }]
  inject:
    - { in: header, key: Authorization, value: "Bearer {{token}}" }
operations:
  - id: create_ticket
    label: Create ticket
    http: { method: POST, path: "/v2/projects/{project}/tickets", bodyType: json }
    inputs:
      - { name: project, type: string, in: path }
      - { name: dry_run, type: boolean, in: query }
      - { name: request_id, type: string, in: header, param: X-Request-Id }
      - { name: title, type: string }
    outputs:
      - { name: id, type: string, from: data.ticket.id }
      - { name: first_tag, type: string, from: data.ticket.tags.0 }
`
	m, err := ParseManifest([]byte(good), false)
	if err != nil {
		t.Fatalf("a well-formed http plugin should pass: %v", err)
	}
	b, _ := json.Marshal(m)
	for _, want := range []string{`"in":"path"`, `"param":"X-Request-Id"`, `"from":"data.ticket.id"`, `"inject":[{"in":"header","key":"Authorization","value":"Bearer {{token}}"}]`} {
		if !strings.Contains(string(b), want) {
			t.Errorf("parsed manifest should keep %s: %s", want, b)
		}
	}

	// the exported contract (what the platform installs) carries all of it
	wire, err := ExportManifestJSON(m, "")
	if err != nil {
		t.Fatal(err)
	}
	var compact bytes.Buffer
	if err := json.Compact(&compact, wire); err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{`"credential_inject":[{"in":"header","key":"Authorization","value":"Bearer {{token}}"}]`, `"in":"path"`, `"param":"X-Request-Id"`, `"from":"data.ticket.id"`} {
		if !strings.Contains(compact.String(), want) {
			t.Errorf("exported contract should carry %s: %s", want, wire)
		}
	}

	for _, tc := range []struct{ name, from, to, want string }{
		{"unknown place", "in: query }", "in: cookie }", `in "cookie" is not one of`},
		{"path input missing from path", "{ name: project, type: string, in: path }", "{ name: projectx, type: string, in: path }", `has no {projectx}`},
		{"path placeholder nobody fills", "{ name: project, type: string, in: path }", "{ name: project2, type: string }", `has {project}, which no input fills`},
		{"param on an http-mapped input passes", "{ name: dry_run, type: boolean, in: query }", "{ name: dry_run, type: boolean, in: query, param: dry-run }", ""},
		{"bad from", "from: data.ticket.id }", "from: data..id }", `is not a dot path`},
		{"from on an input", "{ name: title, type: string }", "{ name: title, type: string, from: x }", `only applies to outputs`},
		{"inject with an unknown field", "Bearer {{token}}", "Bearer {{apikey}}", `{{apikey}}, which is not a credential field`},
		{"inject with a literal secret", "Bearer {{token}}", "Bearer sk-live-123", `a literal value would put the secret in the manifest`},
		{"inject in an unknown place", "{ in: header, key: Authorization", "{ in: cookie, key: Authorization", `in "cookie" is not one of header`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			src := strings.Replace(good, tc.from, tc.to, 1)
			if src == good {
				t.Fatalf("the case did not change the manifest (replace %q)", tc.from)
			}
			_, err := ParseManifest([]byte(src), false)
			if tc.want == "" {
				if err != nil {
					t.Fatalf("should pass: %v", err)
				}
				return
			}
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("want an error containing %q, got %v", tc.want, err)
			}
		})
	}

	// in needs an http mapping to mean anything
	nomap := strings.Replace(good, `http: { method: POST, path: "/v2/projects/{project}/tickets", bodyType: json }`, "protocol: graphql\n    graphql: { query: \"{ x }\" }", 1)
	if _, err := ParseManifest([]byte(nomap), false); err == nil || !strings.Contains(err.Error(), "has no http mapping") {
		t.Fatalf("in without an http mapping should be refused: %v", err)
	}
}
