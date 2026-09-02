// Copyright 2026 The Sokel Authors
// SPDX-License-Identifier: Apache-2.0

package sokelgen

// The language-neutral plugin declaration (manifest.yml / manifest.json).
//
// A Go plugin declares its contract in a schema/ package, which is the Go way: compile time, and a
// misspelled method name fails to build. But the contract itself has nothing to do with Go, and a
// Python or Node author should not have to learn a Go builder API — let alone read Go — just to
// declare a few fields.
//
// So there are two equivalent entry points, producing one IR:
//
//	schema/ package (Go builders) ──┐
//	                                 ├─▶ IR ─▶ render Go / TypeScript / Python
//	manifest.yml (this file) ─────────┘
//
// YAML and JSON are **one format**: YAML is converted to JSON and decoded through the same tags, so
// the two spellings cannot drift and no key can be "supported in YAML but not in JSON". Decoding uses
// DisallowUnknownFields, so a misspelled key fails immediately instead of being silently dropped —
// "written but never took effect" is the classic silent failure of a declarative format.

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"gopkg.in/yaml.v3"
)

// Manifest is one plugin's complete declaration. The field order is the recommended writing order.
type Manifest struct {
	Plugin       PluginDecl      `json:"plugin"`
	Capabilities map[string]bool `json:"capabilities,omitempty"`
	Credential   *CredentialDecl `json:"credential,omitempty"`
	Events       []EventDecl     `json:"events,omitempty"`
	EventsCommon []string        `json:"eventsCommon,omitempty"`
	Operations   []OperationDecl `json:"operations"`
	// Implements is what the plugin implements of the platform's capability interfaces. The
	// operations live **inside** the capability rather than being referenced out of the flat list:
	// that is what lets two capabilities each own a slot called "query" (rowstore / vectorstore)
	// without a binding field, and it makes "which operation serves this capability" a structural
	// fact rather than a self-report.
	//
	// Plain integration plugins leave this out entirely — their manifest is unchanged.
	Implements []CapabilityDecl `json:"implements,omitempty"`
	Codegen    CodegenList      `json:"codegen,omitempty"`

	// path is the manifest's own location, used to resolve relative paths such as doc. It does not
	// come from the file contents.
	path string
}

// PluginDecl is the plugin's identity and its user-facing document.
type PluginDecl struct {
	Name    string `json:"name"`
	Label   string `json:"label,omitempty"`
	Desc    string `json:"desc,omitempty"`
	Version string `json:"version,omitempty"`
	// Doc is the **path** to the user-facing markdown, relative to the manifest's directory.
	// It is not inlined: a document is full of code fences and indentation, and folding that into YAML
	// only makes both harder to read.
	Doc    string `json:"doc,omitempty"`
	DocURL string `json:"docUrl,omitempty"`
}

// CredentialDecl is the credential contract plus how the credential is obtained.
type CredentialDecl struct {
	Auth   *AuthDecl `json:"auth,omitempty"`
	Fields []Field   `json:"fields,omitempty"`
}

// AuthDecl declares collaborative authentication. The steps follow from kind (see contract/auth):
// qr = start+poll, input = start+poll+submit, oauth = answered by the platform, with no handler at all.
type AuthDecl struct {
	Kind     string   `json:"kind"`
	Provider string   `json:"provider,omitempty"`
	Scopes   []string `json:"scopes,omitempty"`
}

// Steps derives the steps from kind. **The author never writes them**: one step too many promises an
// implementation that will never be called, one too few leaves the panel stuck on the missing step,
// and nobody notices either mistake.
func (a AuthDecl) Steps() []string {
	switch a.Kind {
	case "qr":
		return []string{"start", "poll"}
	case "input":
		return []string{"start", "poll", "submit"}
	}
	return nil // oauth: the platform answers it, and the plugin implements no step at all
}

// EventDecl is one event and the contract of its payload.
type EventDecl struct {
	ID     string  `json:"id"`
	Label  string  `json:"label,omitempty"`
	Desc   string  `json:"desc,omitempty"`
	Fields []Field `json:"fields"`
}

// OperationDecl is one operation's contract.
// CapabilityDecl is one implemented capability interface.
//
// The capability name may be hierarchical with a slash (vectorstore/keyword_ngram); the slot is
// separated from it by a dot, so the wire id splits on the **last** dot and a slash never appears
// in a slot name: vectorstore/keyword_ngram.keyword_query.
//
// This mirrors what the platform already does for the auth flow (auth.start / auth.poll), where
// business ids may not contain a dot — the reserved namespace generalised into the rule.
type CapabilityDecl struct {
	Capability string          `json:"capability"`
	Operations []OperationDecl `json:"operations,omitempty"`
}

// Parent is the capability above this one in the hierarchy ("" when it is a root). Sub-capabilities
// of the same parent are mutually exclusive over a slot: both declaring keyword_query is a conflict,
// which is how "bm25 or n-gram, not both" is expressed without a field for it.
func (c CapabilityDecl) Parent() string {
	if i := strings.LastIndex(c.Capability, "/"); i > 0 {
		return c.Capability[:i]
	}
	return ""
}

// WireID is the operation id as it goes over the wire: <capability>.<slot>.
func (c CapabilityDecl) WireID(slot string) string { return c.Capability + "." + slot }

type OperationDecl struct {
	ID         string  `json:"id"`
	Label      string  `json:"label,omitempty"`
	Desc       string  `json:"desc,omitempty"`
	Stream     bool    `json:"stream,omitempty"`
	Internal   bool    `json:"internal,omitempty"`
	TimeoutSec int     `json:"timeoutSec,omitempty"`
	Inputs     []Field `json:"inputs,omitempty"`
	Outputs    []Field `json:"outputs,omitempty"`
}

// CodegenDecl is a generation target. Keeping it in the manifest means `sokel-gen generate <dir>`
// needs no arguments, and CI's check does not have to know what language each plugin is written in.
type CodegenDecl struct {
	Lang string `json:"lang"`          // ts | python
	Out  string `json:"out,omitempty"` // output path, relative to the manifest's directory
}

// CodegenList is one or more generation targets.
//
// Several are allowed because "one declaration, several languages" is the whole point: the reference
// plugin is implemented once in Python and once in Node, and both must follow **the same file**
// rather than two copies drifting apart.
type CodegenList []CodegenDecl

// UnmarshalJSON accepts both a single object and an array, so one target needs no one-element list.
func (c *CodegenList) UnmarshalJSON(b []byte) error {
	trimmed := strings.TrimSpace(string(b))
	if strings.HasPrefix(trimmed, "[") {
		var list []CodegenDecl
		if err := decodeStrict(b, &list); err != nil {
			return err
		}
		*c = list
		return nil
	}
	var one CodegenDecl
	if err := decodeStrict(b, &one); err != nil {
		return err
	}
	*c = CodegenList{one}
	return nil
}

func decodeStrict(b []byte, dst any) error {
	dec := json.NewDecoder(strings.NewReader(string(b)))
	dec.DisallowUnknownFields()
	return dec.Decode(dst)
}

// ManifestNames is the search order for a manifest. Several in one directory is usually a rename that
// left a stale copy behind, so FindManifest reports them all rather than silently taking the first.
//
// The canonical name is manifest.yml — it matches what this concept is called everywhere else
// (this file, docs/manifest.md, the registry index). The sokel.* spellings are the pre-2026-09-02
// name, still accepted so that anyone who cloned during the first open-source week is not broken;
// nothing emits them any more. Delete that tail whenever the grace period is over.
var ManifestNames = []string{
	"manifest.yml", "manifest.yaml", "manifest.json",
	"sokel.yaml", "sokel.yml", "sokel.json", // deprecated 2026-09-02
}

// FindManifest looks for a plugin manifest in a directory. Finding none returns "" rather than an
// error: a Go plugin takes the schema/ path instead.
func FindManifest(dir string) (string, error) {
	var found []string
	for _, n := range ManifestNames {
		p := filepath.Join(dir, n)
		if fi, err := os.Stat(p); err == nil && !fi.IsDir() {
			found = append(found, p)
		}
	}
	if len(found) > 1 {
		return "", fmt.Errorf("%s holds more than one manifest (%s) — keep one", dir, strings.Join(found, " / "))
	}
	if len(found) == 0 {
		return "", nil
	}
	return found[0], nil
}

// LoadManifest reads a manifest (YAML or JSON, by file extension) and validates it.
func LoadManifest(path string) (*Manifest, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("reading %s: %w", path, err)
	}
	m, err := ParseManifest(raw, strings.HasSuffix(path, ".json"))
	if err != nil {
		return nil, fmt.Errorf("%s: %w", path, err)
	}
	m.path = path
	return m, nil
}

// ParseManifest parses manifest bytes; asJSON=false reads them as YAML.
//
// YAML is converted to JSON and decoded rather than given a second set of yaml tags: two sets of tags
// are two declarations, and eventually `valueType` would be recognised in JSON while YAML lowercased
// it to `valuetype` and rejected it.
func ParseManifest(raw []byte, asJSON bool) (*Manifest, error) {
	data := raw
	if !asJSON {
		var node any
		if err := yaml.Unmarshal(raw, &node); err != nil {
			return nil, fmt.Errorf("parsing YAML: %w", err)
		}
		b, err := json.Marshal(node)
		if err != nil {
			return nil, fmt.Errorf("converting YAML to JSON: %w", err)
		}
		data = b
	}
	// Key normalisation: the registration payload in the protocol document is snake_case
	// (events_common, doc_url) while the Field layer is camelCase (valueType, oneOf). Both are
	// accepted, so copying a line out of the protocol never lands on an "unknown field" — that would be
	// pure memory load, not discipline.
	var node any
	if err := json.Unmarshal(data, &node); err != nil {
		return nil, fmt.Errorf("parsing the declaration: %w", err)
	}
	node = applyAliases(node)
	data, err := json.Marshal(node)
	if err != nil {
		return nil, err
	}

	var m Manifest
	dec := json.NewDecoder(strings.NewReader(string(data)))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&m); err != nil {
		return nil, fmt.Errorf("parsing the declaration: %w", err)
	}
	m.normalize()
	if err := m.Validate(); err != nil {
		return nil, err
	}
	return &m, nil
}

// keyAliases maps the snake_case spellings onto the keys the structs accept.
var keyAliases = map[string]string{
	"events_common": "eventsCommon",
	"doc_url":       "docUrl",
	"timeout_sec":   "timeoutSec",
	"value_type":    "valueType",
	"item_type":     "itemType",
	"one_of":        "oneOf",
	"go_type":       "goType",
}

func applyAliases(node any) any {
	switch t := node.(type) {
	case map[string]any:
		out := make(map[string]any, len(t))
		for k, v := range t {
			if alias, ok := keyAliases[k]; ok {
				k = alias
			}
			out[k] = applyAliases(v)
		}
		return out
	case []any:
		for i, v := range t {
			t[i] = applyAliases(v)
		}
		return t
	}
	return node
}

// Dir is the manifest's directory; doc and output paths are relative to it.
func (m *Manifest) Dir() string {
	if m.path == "" {
		return "."
	}
	return filepath.Dir(m.path)
}

// Path is the manifest's own path, for error messages.
func (m *Manifest) Path() string { return m.path }

// —— normalisation ——

// normalize expands the shorthands so everything downstream sees only the protocol's own types.
//
//	int    -> number + goType:int (other languages generate int rather than a float from it)
//	files  -> array + itemType:file (a file list has exactly one spelling)
//	ints   → array + itemType:number + goType:int
func (m *Manifest) normalize() {
	m.normalizeSugar()
	m.resolveGoTypeRefs()
}

// normalizeSugar expands the shorthands.
func (m *Manifest) normalizeSugar() {
	for i := range m.Operations {
		normalizeFields(m.Operations[i].Inputs)
		normalizeFields(m.Operations[i].Outputs)
	}
	// Capability slots take the same shorthands as ordinary operations. Forgetting this pass is
	// how "int is a documented shorthand" and "unknown type int" end up in the same error message.
	for i := range m.Implements {
		for j := range m.Implements[i].Operations {
			normalizeFields(m.Implements[i].Operations[j].Inputs)
			normalizeFields(m.Implements[i].Operations[j].Outputs)
		}
	}
	for i := range m.Events {
		normalizeFields(m.Events[i].Fields)
	}
	if m.Credential != nil {
		normalizeFields(m.Credential.Fields)
	}
}

// resolveGoTypeRefs makes `goType: Profile` name a structure so a later reference to that name
// **need not repeat its fields** — an output echoing an input's structure being the common case.
//
// Repeating them is the real risk: the two copies drift, and then the platform sees two structures
// with the same name and the same shape but different contents, with nothing to say which is right.
func (m *Manifest) resolveGoTypeRefs() {
	defs := map[string][]Field{}
	m.walkFields(func(f *Field) {
		if f.GoType != "" && !isIntGoType(f.GoType) && len(f.Fields) > 0 {
			if _, ok := defs[f.GoType]; !ok {
				defs[f.GoType] = f.Fields
			}
		}
	})
	if len(defs) == 0 {
		return
	}
	m.walkFields(func(f *Field) {
		if f.GoType == "" || isIntGoType(f.GoType) || len(f.Fields) > 0 || f.ValueType != nil || f.Opaque {
			return
		}
		if fields, ok := defs[f.GoType]; ok {
			f.Fields = fields
		}
	})
}

// walkFields visits every field in the declaration, including nested ones, oneOf branches and valueType.
func (m *Manifest) walkFields(fn func(*Field)) {
	for i := range m.Operations {
		walkFieldList(m.Operations[i].Inputs, fn)
		walkFieldList(m.Operations[i].Outputs, fn)
	}
	for i := range m.Implements {
		for j := range m.Implements[i].Operations {
			walkFieldList(m.Implements[i].Operations[j].Inputs, fn)
			walkFieldList(m.Implements[i].Operations[j].Outputs, fn)
		}
	}
	for i := range m.Events {
		walkFieldList(m.Events[i].Fields, fn)
	}
	if m.Credential != nil {
		walkFieldList(m.Credential.Fields, fn)
	}
}

func walkFieldList(fs []Field, fn func(*Field)) {
	for i := range fs {
		fn(&fs[i])
		walkFieldList(fs[i].Fields, fn)
		for j := range fs[i].OneOf {
			walkFieldList(fs[i].OneOf[j].Fields, fn)
		}
		if fs[i].ValueType != nil {
			walkFieldList([]Field{*fs[i].ValueType}, fn)
		}
	}
}

func normalizeFields(fs []Field) {
	for i := range fs {
		f := &fs[i]
		switch f.Type {
		case "int":
			f.Type, f.GoType = "number", "int"
		case "files":
			f.Type, f.ItemType = "array", "file"
		case "ints":
			f.Type, f.ItemType, f.GoType = "array", "number", "int"
		case "strings":
			f.Type, f.ItemType = "array", "string"
		}
		if f.ValueType != nil {
			vt := *f.ValueType
			normalizeOne(&vt)
			f.ValueType = &vt
		}
		normalizeFields(f.Fields)
		for j := range f.OneOf {
			normalizeFields(f.OneOf[j].Fields)
		}
	}
}

func normalizeOne(f *Field) {
	tmp := []Field{*f}
	normalizeFields(tmp)
	*f = tmp[0]
}

// —— validation ——

var (
	opIDRe    = regexp.MustCompile(`^[a-z][a-z0-9_]*$`)
	fieldIDRe = regexp.MustCompile(`^[a-zA-Z_][a-zA-Z0-9_]*$`)
	// capIDRe: capability names are slash-joined segments. The slot is appended with a dot, so a
	// name never contains one — the wire id splits on the last dot.
	capIDRe = regexp.MustCompile(`^[a-z][a-z0-9_]*(/[a-z][a-z0-9_]*)*$`)
)

// wireTypes are the types the protocol knows. Anything else in a declaration is an error: the
// platform degrades an unknown type to "treat it as a string", which is among the hardest failures to
// track down.
var wireTypes = map[string]bool{
	"string": true, "text": true, "number": true, "boolean": true,
	"file": true, "json": true, "array": true, "enum": true, "secret": true,
}

// Validate checks everything and **reports every problem at once** rather than stopping at the first:
// whoever edits a declaration usually changes several things, and going back and forth one error at a
// time is both slower and easier to leave half-done.
func (m *Manifest) Validate() error {
	var errs []string
	add := func(format string, args ...any) { errs = append(errs, fmt.Sprintf(format, args...)) }

	if strings.TrimSpace(m.Plugin.Name) == "" {
		add("plugin.name must not be empty")
	}
	if len(m.Operations) == 0 && len(m.Events) == 0 {
		add("neither operations nor events — this plugin does nothing")
	}
	seen := map[string]bool{}
	for _, op := range m.Operations {
		switch {
		case op.ID == "":
			add("an operation has no id")
		case strings.Contains(op.ID, "."):
			add("operation id %q is in the platform's reserved namespace (it contains a dot) — declare auth flows under credential.auth", op.ID)
		case !opIDRe.MatchString(op.ID):
			add("operation id %q is invalid; it must match ^[a-z][a-z0-9_]*$", op.ID)
		case op.ID == "auth_start" || op.ID == "auth_submit" || op.ID == "auth_poll":
			add("operation id %q is the old auth-flow convention; it is declarative now: credential.auth", op.ID)
		case seen[op.ID]:
			add("duplicate operation id %q", op.ID)
		}
		seen[op.ID] = true
		errs = append(errs, validateFields(fmt.Sprintf("operation %q inputs", op.ID), op.Inputs)...)
		errs = append(errs, validateFields(fmt.Sprintf("operation %q outputs", op.ID), op.Outputs)...)
	}

	// —— implements ——
	//
	// The SDK can only check **structure** here: names, duplicates, slot conflicts, field shapes.
	// Whether a capability's slots match the platform's definition is the platform's check at
	// registration — the catalogue lives there, not in the SDK.
	seenCap := map[string]bool{}
	slotOwner := map[string]string{} // "<parent>|<slot>" -> the capability that took it
	for _, c := range m.Implements {
		switch {
		case c.Capability == "":
			add("an entry under implements has no capability name")
			continue
		case !capIDRe.MatchString(c.Capability):
			add("capability %q is invalid; it must match %s (segments joined by /)", c.Capability, capIDRe)
			continue
		case seenCap[c.Capability]:
			add("capability %q is declared twice", c.Capability)
			continue
		}
		seenCap[c.Capability] = true
		if len(c.Operations) == 0 {
			add("capability %q declares no operations — declaring it means filling its slots", c.Capability)
		}
		slots := map[string]bool{}
		for _, op := range c.Operations {
			switch {
			case op.ID == "":
				add("capability %q has a slot with no id", c.Capability)
				continue
			case !opIDRe.MatchString(op.ID):
				// A slot never carries the separators: the capability supplies them.
				add("capability %q: slot id %q is invalid; it must match ^[a-z][a-z0-9_]*$ (the capability supplies the . and /)", c.Capability, op.ID)
				continue
			case slots[op.ID]:
				add("capability %q fills slot %q twice", c.Capability, op.ID)
				continue
			}
			slots[op.ID] = true
			// Sub-capabilities of one parent are mutually exclusive over a slot. This is how
			// "bm25 or n-gram, not both" is expressed **without a field for it**: two of them
			// reaching for keyword_query is a conflict, and we say so rather than picking one.
			if p := c.Parent(); p != "" {
				key := p + "|" + op.ID
				if prev, taken := slotOwner[key]; taken {
					add("%q and %q both fill slot %q under %q — they are alternatives, declare one", prev, c.Capability, op.ID, p)
				} else {
					slotOwner[key] = c.Capability
				}
			}
			errs = append(errs, validateFields(fmt.Sprintf("capability %q slot %q inputs", c.Capability, op.ID), op.Inputs)...)
			errs = append(errs, validateFields(fmt.Sprintf("capability %q slot %q outputs", c.Capability, op.ID), op.Outputs)...)
		}
	}

	eventIDs := map[string]bool{}
	for _, e := range m.Events {
		if e.ID == "" {
			add("an event has no id")
		} else if !opIDRe.MatchString(e.ID) {
			add("event id %q is invalid; it must match ^[a-z][a-z0-9_]*$", e.ID)
		} else if eventIDs[e.ID] {
			add("duplicate event id %q", e.ID)
		}
		eventIDs[e.ID] = true
		errs = append(errs, validateFields(fmt.Sprintf("event %q fields", e.ID), e.Fields)...)
	}
	errs = append(errs, m.validateCommon(eventIDs)...)

	if m.Credential != nil {
		errs = append(errs, validateFields("credential.fields", m.Credential.Fields)...)
		if a := m.Credential.Auth; a != nil {
			switch a.Kind {
			case "qr", "input":
			case "oauth":
				if a.Provider == "" {
					add("credential.auth.kind=oauth requires a provider")
				}
			default:
				add("credential.auth.kind %q is invalid (qr / input / oauth)", a.Kind)
			}
		}
	}
	for _, c := range m.Codegen {
		switch c.Lang {
		case "ts", "python":
		default:
			add("codegen.lang %q is invalid (ts / python)", c.Lang)
		}
	}
	if len(errs) > 0 {
		return fmt.Errorf("the declaration has %d problems:\n  - %s", len(errs), strings.Join(errs, "\n  - "))
	}
	return nil
}

// validateCommon requires each common field to exist in **every** event with the same type — the same
// rule as contract.ValidateCommonFields on the Go side. Inferring the intersection will not do: an
// added event that omits a field would silently shrink the set and break existing workflows.
func (m *Manifest) validateCommon(eventIDs map[string]bool) []string {
	if len(m.EventsCommon) == 0 {
		return nil
	}
	var errs []string
	if len(m.Events) == 0 {
		return []string{"eventsCommon is declared but there are no events"}
	}
	reserved := map[string]bool{"_event": true, "event": true, "input": true, "credential_id": true}
	for _, name := range m.EventsCommon {
		if reserved[name] {
			errs = append(errs, fmt.Sprintf("common field %q collides with a platform-reserved key", name))
			continue
		}
		if eventIDs[name] {
			errs = append(errs, fmt.Sprintf("common field %q collides with an event id", name))
			continue
		}
		var ref *Field
		for _, e := range m.Events {
			hit := findField(e.Fields, name)
			if hit == nil {
				errs = append(errs, fmt.Sprintf("common field %q does not exist in the contract of event %q", name, e.ID))
				break
			}
			if ref == nil {
				ref = hit
			} else if ref.Type != hit.Type {
				errs = append(errs, fmt.Sprintf("common field %q has different types across events (%s vs %s)", name, ref.Type, hit.Type))
				break
			}
		}
	}
	return errs
}

func findField(fs []Field, name string) *Field {
	for i := range fs {
		if fs[i].Name == name {
			return &fs[i]
		}
	}
	return nil
}

func validateFields(where string, fs []Field) []string {
	var errs []string
	seen := map[string]bool{}
	for _, f := range fs {
		switch {
		case f.Name == "":
			errs = append(errs, where+": a field has no name")
		case !fieldIDRe.MatchString(f.Name):
			errs = append(errs, fmt.Sprintf("%s: field name %q is invalid (letters, digits and underscore; must not start with a digit)", where, f.Name))
		case seen[f.Name]:
			errs = append(errs, fmt.Sprintf("%s: duplicate field name %q", where, f.Name))
		}
		seen[f.Name] = true
		errs = append(errs, validateField(where+"."+f.Name, f)...)
	}
	return errs
}

func validateField(where string, f Field) []string {
	var errs []string
	if f.Type == "" {
		return append(errs, where+" has no type")
	}
	if !wireTypes[f.Type] {
		errs = append(errs, fmt.Sprintf("%s has an unknown type %q (string/text/number/boolean/file/json/array/enum/secret; shorthands: int/files/ints/strings)", where, f.Type))
	}
	for _, t := range f.Types {
		if !wireTypes[t] {
			errs = append(errs, fmt.Sprintf("%s lists an unknown type %q in `types`", where, t))
		}
	}
	if f.Type == "enum" && len(f.Options) == 0 {
		errs = append(errs, where+" is an enum but has no options")
	}
	if f.Type != "enum" && len(f.Options) > 0 {
		errs = append(errs, where+" is not an enum but has options")
	}
	if f.ValueType != nil && len(f.Fields) > 0 {
		errs = append(errs, where+" sets both fields and valueType — they are mutually exclusive (fixed keys use fields, runtime keys use valueType)")
	}
	// goType is both "the name of a structure" and "a reference to it". Referencing a name nobody
	// defined generates an empty shell, and at runtime that shows up as fields mysteriously vanishing.
	if f.GoType != "" && !isIntGoType(f.GoType) && len(f.Fields) == 0 && f.ValueType == nil && !f.Opaque &&
		(f.Type == "json" || f.Type == "array") {
		errs = append(errs, fmt.Sprintf("%s references the structure %q, but nothing declares its fields", where, f.GoType))
	}
	if f.Opaque {
		// The same discipline as field.Opaque(reason) on the Go side: declaring no structure requires a
		// reason. "I couldn't be bothered" and "this genuinely has no structure" look identical in a file,
		// and the reason is the only thing that separates them.
		if f.Type != "json" && f.Type != "array" {
			errs = append(errs, where+": only json / array may be marked opaque")
		}
		if strings.TrimSpace(f.Desc) == "" {
			errs = append(errs, where+" is opaque but has no desc — declaring no structure requires a reason")
		}
		if len(f.Fields) > 0 || f.ValueType != nil {
			errs = append(errs, where+" is opaque and also declares structure — pick one")
		}
	}
	for _, v := range f.OneOf {
		if v.Name == "" {
			errs = append(errs, where+": a oneOf branch has no name")
		}
		if v.Type == "" {
			errs = append(errs, fmt.Sprintf("%s: oneOf branch %q has no type", where, v.Name))
		}
		errs = append(errs, validateFields(where+".oneOf."+v.Name, v.Fields)...)
	}
	errs = append(errs, validateFields(where, f.Fields)...)
	if f.ValueType != nil {
		errs = append(errs, validateField(where+".valueType", *f.ValueType)...)
	}
	return errs
}

// —— conversion to the IR ——

// FlatOp is one operation as it exists on the wire: a top-level business operation, or a capability
// slot whose id carries its capability.
type FlatOp struct {
	// Decl carries the wire id in Decl.ID: a plain operation keeps its own, a capability slot gets
	// <capability>.<slot>.
	Decl       OperationDecl
	Capability string // "" for a plain business operation
	// TypeName is the exported base for generated types. It must carry the capability, not just the
	// slot: rowstore.query and vectorstore.query are both slot "query", so deriving from the slot
	// alone generates two QueryIn types and the second silently wins.
	TypeName string
}

// AllOperations flattens business operations and capability slots into one list.
//
// **Every downstream consumer must go through here.** Wiring `implements` in meant touching four
// separate passes that each walked m.Operations on their own (fields, sugar, IR, contract JSON);
// three of them were found only because a guard went red. One entry point so the fourth does not
// get missed next time.
func (m *Manifest) AllOperations() []FlatOp {
	out := make([]FlatOp, 0, len(m.Operations))
	for _, op := range m.Operations {
		out = append(out, FlatOp{Decl: op, TypeName: exportName(op.ID)})
	}
	for _, c := range m.Implements {
		for _, op := range c.Operations {
			d := op
			d.ID = c.WireID(op.ID)
			out = append(out, FlatOp{
				Decl: d, Capability: c.Capability,
				TypeName: exportName(capTypePrefix(c.Capability) + "_" + op.ID),
			})
		}
	}
	return out
}

// Ops turns the declaration into the generator's IR. Type names derive from the **operation id**, the
// same rule the Go path uses,
// so one contract produces the same names no matter which entry point it came through.
func (m *Manifest) Ops() []OpIO {
	all := m.AllOperations()
	out := make([]OpIO, 0, len(all))
	for _, fo := range all {
		in, outs := fo.Decl.Inputs, fo.Decl.Outputs
		if in == nil {
			in = []Field{}
		}
		if outs == nil {
			outs = []Field{}
		}
		out = append(out, OpIO{
			OpID: fo.Decl.ID, Label: fo.Decl.Label, Desc: fo.Decl.Desc, Stream: fo.Decl.Stream,
			InType: fo.TypeName + "In", OutType: fo.TypeName + "Out",
			Inputs: in, Outputs: outs,
		})
	}
	return out
}

// capTypePrefix turns a capability name into something exportName can use: the separators a
// capability may contain (slash, dot) become underscores.
func capTypePrefix(cap string) string {
	return strings.NewReplacer("/", "_", ".", "_", "-", "_").Replace(cap)
}

// EventIOs turns event declarations into the IR.
func (m *Manifest) EventIOs() []EventIO {
	out := make([]EventIO, 0, len(m.Events))
	for _, e := range m.Events {
		fs := e.Fields
		if fs == nil {
			fs = []Field{}
		}
		out = append(out, EventIO{ID: e.ID, Label: e.Label, Desc: e.Desc, Fields: fs, TypeName: exportName(e.ID) + "Event"})
	}
	return out
}

// DocMarkdown reads the user-facing document that plugin.doc points at, or "" when none is declared.
func (m *Manifest) DocMarkdown() (string, error) {
	if m.Plugin.Doc == "" {
		return "", nil
	}
	p := m.Plugin.Doc
	if !filepath.IsAbs(p) {
		p = filepath.Join(m.Dir(), p)
	}
	b, err := os.ReadFile(p)
	if err != nil {
		return "", fmt.Errorf("reading the user-facing doc %s: %w", p, err)
	}
	return string(b), nil
}

// UnmarshalJSON accepts both spellings of an enum candidate:
//
//	options: [asc, desc]                       # the value reads fine, so no display name is needed
//	options: [{value: v1, label: Voice one}]   # the value is a code, so it needs a display name
//
// They map one to one onto the protocol's two forms (field.Opt's variadic arguments on the Go side).
func (o *Option) UnmarshalJSON(b []byte) error {
	var s string
	if err := json.Unmarshal(b, &s); err == nil {
		o.Value = s
		return nil
	}
	var alias struct {
		Value string `json:"value"`
		Label string `json:"label,omitempty"`
	}
	dec := json.NewDecoder(strings.NewReader(string(b)))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&alias); err != nil {
		return fmt.Errorf("an enum option must be a string or {value,label}: %w", err)
	}
	if alias.Value == "" {
		return fmt.Errorf("an enum option is missing `value`")
	}
	o.Value, o.Label = alias.Value, alias.Label
	return nil
}
