// Copyright 2026 The Sokel Authors
// SPDX-License-Identifier: Apache-2.0

package sokelgen

// Two things every language renderer shares: collecting named structs, and rendering contract
// literals.
//
// Collection is factored out because "a struct referenced in several places is defined once" is
// something all three languages need, and writing it three times would mean three different sets of
// gaps. Go reuses via goType; a language that skipped collection would emit a string of identically
// shaped but unrelated anonymous types, leaving the author converting back and forth — exactly what
// this is meant to eliminate.

import (
	"encoding/json"
	"fmt"
	"sort"
	"strconv"
	"strings"
)

// namedModel is one named struct: a json or array field whose goType supplies a name.
type namedModel struct {
	Name   string
	Fields []Field
}

// collectModels recursively gathers every named struct, keeping the first of any repeated name. Two
// different shapes under one name is a declaration error, but catching it is Validate's job — checking
// again while rendering would only produce two inconsistent messages for one problem.
func collectModels(fields []Field, into map[string][]Field) {
	for _, f := range fields {
		if f.GoType != "" && !isIntGoType(f.GoType) && len(f.Fields) > 0 {
			if _, ok := into[f.GoType]; !ok {
				into[f.GoType] = f.Fields
			}
		}
		collectModels(f.Fields, into)
		for _, v := range f.OneOf {
			if name := variantModelName(v); name != "" && len(v.Fields) > 0 {
				if _, ok := into[name]; !ok {
					into[name] = v.Fields
				}
			}
			collectModels(v.Fields, into)
		}
		if f.ValueType != nil {
			collectModels([]Field{*f.ValueType}, into)
		}
	}
}

// variantModelName is a oneOf branch's type name. For an array branch, goType names **the element
// type**.
func variantModelName(v OneOfVariant) string {
	if v.GoType != "" {
		return v.GoType
	}
	return exportName(v.Name)
}

// sortedModels emits in a stable order: the output has to be deterministic, or every run produces a
// meaningless diff.
func sortedModels(m map[string][]Field) []namedModel {
	names := make([]string, 0, len(m))
	for n := range m {
		names = append(names, n)
	}
	sort.Strings(names)
	out := make([]namedModel, 0, len(names))
	for _, n := range names {
		out = append(out, namedModel{Name: n, Fields: m[n]})
	}
	return out
}

// anonModelName names an anonymous nested struct as the owning type plus the field name, giving nested
// structs without a goType a stable name rather than an inline literal at every use.
func anonModelName(owner, field string) string { return owner + exportName(field) }

// contractJSON is the contract dictionary — the contract portion of the registration payload.
// Rendering it as JSON is deliberate: all three languages embed **the same JSON**, so the golden file
// can compare byte for byte.
func contractJSON(m *Manifest, doc string) (map[string]any, error) {
	out := map[string]any{"name": m.Plugin.Name}
	// label, desc and version never reach the registration payload (the platform's display name comes
	// from the plugin catalogue), but they do reach the output: anything declared in manifest.yml has to be
	// **visible** somewhere, or changing it would not even turn check red.
	// org travels with the contract as a **claim**: the registry is what turns it into an identity
	// (<org>/<name>) and a trust tier. The platform must not read a trust level out of here.
	putIfSet(out, "org", m.Plugin.Org)
	putIfSet(out, "label", m.Plugin.Label)
	putIfSet(out, "desc", m.Plugin.Desc)
	putIfSet(out, "version", m.Plugin.Version)
	all := m.AllOperations()
	ops := make([]map[string]any, 0, len(all))
	for _, fo := range all {
		op := fo.Decl
		entry := map[string]any{"id": op.ID, "inputs": nonNil(op.Inputs), "outputs": nonNil(op.Outputs)}
		if fo.Capability != "" {
			// The platform needs to know which capability a slot fills — that is the whole point of
			// the declaration, and the dotted id alone would make it guess by splitting a string.
			entry["capability"] = fo.Capability
		}
		putIfSet(entry, "label", op.Label)
		putIfSet(entry, "desc", op.Desc)
		if op.Stream {
			entry["stream"] = true
		}
		if op.Internal {
			entry["internal"] = true
		}
		if op.TimeoutSec > 0 {
			entry["timeoutSec"] = op.TimeoutSec
		}
		ops = append(ops, entry)
	}
	// The auth flow's three reserved operations are **part of the contract**: the platform panel calls
	// them via /credentials/{id}/auth/{step}, and without them in the contract it would not know which
	// parameters to send. Declaring auth adds them automatically; the author never writes them.
	if m.Credential != nil && m.Credential.Auth != nil {
		ops = append(ops, authOperations(m.Credential.Auth.Steps())...)
	}
	out["operations"] = ops
	if m.Credential != nil && len(m.Credential.Fields) > 0 {
		out["credential_schema"] = m.Credential.Fields
	}
	if len(m.Events) > 0 {
		evs := make([]map[string]any, 0, len(m.Events))
		for _, e := range m.Events {
			entry := map[string]any{"id": e.ID, "fields": nonNil(e.Fields)}
			putIfSet(entry, "label", e.Label)
			putIfSet(entry, "desc", e.Desc)
			evs = append(evs, entry)
		}
		out["events"] = evs
	}
	// Translations ride along with the contract: the platform is the side that renders plugin-supplied
	// text, so it is the side that needs them. The source string is the key, and a language the viewer
	// asked for but the plugin does not ship falls back to the source — the original shows, never a blank.
	if len(m.Locales) > 0 {
		out["locales"] = m.Locales
	}
	// deployment 也随契约走：平台装完要据此渲染一键启动命令，而它拿到的只有这份契约
	// （registry 发布的是契约，不是 manifest 原文）。漏了它，「需自部署」的插件装完
	// 就只剩一句「去接入组里找命令」——正是这次要治的那个体验。
	if m.Deployment != nil && len(m.Deployment.Targets) > 0 {
		out["deployment"] = m.Deployment
	}
	if len(m.EventsCommon) > 0 {
		common := make([]Field, 0, len(m.EventsCommon))
		for _, name := range m.EventsCommon {
			// Validation has already confirmed that every common field exists in every event with the same
			// type, so the first one will do
			if f := findField(m.Events[0].Fields, name); f != nil {
				common = append(common, *f)
			}
		}
		out["events_common"] = common
	}
	if m.Credential != nil && m.Credential.Auth != nil {
		a := m.Credential.Auth
		flow := map[string]any{"kind": a.Kind}
		if steps := a.Steps(); len(steps) > 0 {
			flow["steps"] = steps
		}
		out["auth_flow"] = flow
		if a.Kind == "oauth" {
			oauth := map[string]any{"provider": a.Provider}
			if len(a.Scopes) > 0 {
				oauth["scopes"] = a.Scopes
			}
			out["oauth"] = oauth
		}
	}
	if len(m.Capabilities) > 0 {
		out["capabilities"] = m.Capabilities
	}
	putIfSet(out, "doc", doc)
	putIfSet(out, "doc_url", m.Plugin.DocURL)

	// A round trip through JSON normalises omitempty-bearing structs such as Field into plain maps, so
	// every language renderer sees the same data without having to understand Go struct tags.
	b, err := json.Marshal(out)
	if err != nil {
		return nil, err
	}
	var normalized map[string]any
	if err := json.Unmarshal(b, &normalized); err != nil {
		return nil, err
	}
	return normalized, nil
}

// authOperations is the contract of the auth flow's reserved operations. Its shape matches the Go SDK's
// auth.go word for word: the platform panel builds its requests from this contract, and if the three
// SDKs described different shapes the panel would work with only one of them.
func authOperations(steps []string) []map[string]any {
	specs := map[string]map[string]any{
		"start": {
			"id": "auth.start", "label": "Start authentication", "internal": true,
			"inputs": []Field{},
			"outputs": []Field{
				{Name: "auth_id", Label: "Auth ID", Type: "string", Required: true},
				{Name: "challenge", Label: "Challenge", Type: "json", Required: true,
					Opaque: true, Desc: "The challenge depends on kind: qr carries qr_image, input carries prompt"},
				{Name: "expires_in", Label: "Expires in (s)", Type: "number", GoType: "int"},
			},
		},
		"poll": {
			"id": "auth.poll", "label": "Poll authentication", "internal": true,
			"inputs": []Field{{Name: "auth_id", Label: "Auth ID", Type: "string", Required: true}},
			"outputs": []Field{
				{Name: "status", Label: "Status", Type: "string", Required: true,
					Desc: "pending / scanned / confirmed / expired"},
				{Name: "session", Label: "Session", Type: "json", Opaque: true,
					Desc: "The credential content once confirmed; its shape is the plugin's own. The platform writes it into the credential row and strips it from the response"},
			},
		},
		"submit": {
			"id": "auth.submit", "label": "Submit authentication input", "internal": true,
			"inputs": []Field{
				{Name: "auth_id", Label: "Auth ID", Type: "string", Required: true},
				{Name: "input", Label: "User input", Type: "string", Required: true},
			},
			"outputs": []Field{{Name: "ok", Label: "Submitted", Type: "boolean", Required: true}},
		},
	}
	out := make([]map[string]any, 0, len(steps))
	for _, s := range steps {
		if spec, ok := specs[s]; ok {
			out = append(out, spec)
		}
	}
	return out
}

func nonNil(fs []Field) []Field {
	if fs == nil {
		return []Field{} // an empty array rather than null, so neither the platform nor the frontend guards against null
	}
	return fs
}

func putIfSet(m map[string]any, key, val string) {
	if val != "" {
		m[key] = val
	}
}

// —— literal rendering ——

// renderJSONLiteral renders JSON, which TS can use as-is because a JSON literal is a valid TS object
// literal. Sorted keys and fixed indentation keep the output deterministic.
func renderJSONLiteral(v any, indent int) string {
	pad := strings.Repeat("  ", indent)
	switch t := v.(type) {
	case nil:
		return "null"
	case bool:
		return strconv.FormatBool(t)
	case float64:
		return trimFloat(t)
	case int:
		return strconv.Itoa(t)
	case string:
		return jsonQuote(t)
	case []any:
		if len(t) == 0 {
			return "[]"
		}
		var b strings.Builder
		b.WriteString("[\n")
		for i, item := range t {
			b.WriteString(pad + "  " + renderJSONLiteral(item, indent+1))
			if i < len(t)-1 {
				b.WriteString(",")
			}
			b.WriteString("\n")
		}
		b.WriteString(pad + "]")
		return b.String()
	case map[string]any:
		if len(t) == 0 {
			return "{}"
		}
		keys := make([]string, 0, len(t))
		for k := range t {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		var b strings.Builder
		b.WriteString("{\n")
		for i, k := range keys {
			b.WriteString(pad + "  " + jsonQuote(k) + ": " + renderJSONLiteral(t[k], indent+1))
			if i < len(keys)-1 {
				b.WriteString(",")
			}
			b.WriteString("\n")
		}
		b.WriteString(pad + "}")
		return b.String()
	}
	b, _ := json.Marshal(v)
	return string(b)
}

// renderPyLiteral renders a Python literal, where the three words true/false/null differ from JSON.
func renderPyLiteral(v any, indent int) string {
	pad := strings.Repeat("    ", indent)
	switch t := v.(type) {
	case nil:
		return "None"
	case bool:
		if t {
			return "True"
		}
		return "False"
	case float64:
		return trimFloat(t)
	case string:
		return jsonQuote(t) // JSON's escaping rules are a subset of Python string literals'
	case []any:
		if len(t) == 0 {
			return "[]"
		}
		var b strings.Builder
		b.WriteString("[\n")
		for _, item := range t {
			b.WriteString(pad + "    " + renderPyLiteral(item, indent+1) + ",\n")
		}
		b.WriteString(pad + "]")
		return b.String()
	case map[string]any:
		if len(t) == 0 {
			return "{}"
		}
		keys := make([]string, 0, len(t))
		for k := range t {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		var b strings.Builder
		b.WriteString("{\n")
		for _, k := range keys {
			b.WriteString(pad + "    " + jsonQuote(k) + ": " + renderPyLiteral(t[k], indent+1) + ",\n")
		}
		b.WriteString(pad + "}")
		return b.String()
	}
	b, _ := json.Marshal(v)
	return string(b)
}

// jsonQuote uses encoding/json rather than strconv.Quote, because the latter escapes non-ASCII to
// \uXXXX — and a contract whose labels are not in English would come out as a screen full of escapes
// that nobody can read.
func jsonQuote(s string) string {
	var b strings.Builder
	enc := json.NewEncoder(&b)
	enc.SetEscapeHTML(false)
	_ = enc.Encode(s)
	return strings.TrimRight(b.String(), "\n")
}

func trimFloat(f float64) string {
	if f == float64(int64(f)) {
		return strconv.FormatInt(int64(f), 10)
	}
	return strconv.FormatFloat(f, 'g', -1, 64)
}

// docComment splits a description into comment lines. prefix is the language's comment marker.
func docComment(prefix, s string) string {
	if s == "" {
		return ""
	}
	var b strings.Builder
	for _, line := range strings.Split(strings.TrimRight(s, "\n"), "\n") {
		// Blank lines carry no trailing space: invisible whitespace in generated output is both ugly and a
		// source of dirty diffs
		fmt.Fprintf(&b, "%s\n", strings.TrimRight(prefix+line, " \t"))
	}
	return b.String()
}
