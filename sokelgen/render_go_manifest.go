// Copyright 2026 The Sokel Authors
// SPDX-License-Identifier: Apache-2.0

package sokelgen

import (
	"fmt"
	"go/format"
	"sort"
	"strconv"
	"strings"
)

// Rendering Go from a manifest.
//
// A Go contract can be declared two ways, and **neither is the privileged one**: a schema/ package
// (executable, reuses existing Go types) or manifest.yml (language-neutral, and the only artifact
// distribution wants). Requiring the schema package meant a Go author could not start from the same
// file a Python or TypeScript author starts from, and that a plugin ported to Go had to have its
// contract retyped by hand — two declarations that agree only as long as someone keeps them agreeing.
//
// What comes out is deliberately **the same public API as the schema path**: OnXxx per operation,
// RegisterCredential, DeclareEvents, TriggerXxx. main.go therefore looks identical either way, and a
// plugin can move between the two without its implementation noticing.
//
// The difference is only where the contract values come from: here they are rendered as
// contract.Operation literals, rather than read back out of a compiled schema package.

// GoFromManifest is the set of files rendered for a manifest-declared Go plugin.
// Keys are file names; the caller writes or compares them.
type GoFromManifest map[string]string

// RenderGoFromManifest renders the whole Go shell for a manifest-declared plugin.
//
// pkg is the Go package the files join — "main" for a standalone plugin.
func RenderGoFromManifest(m *Manifest, doc, pkg string) (GoFromManifest, error) {
	if pkg == "" {
		pkg = "main"
	}
	fos := m.AllOperations()
	if len(fos) == 0 {
		return nil, fmt.Errorf("the manifest declares no operations")
	}
	ops := make([]OpIO, 0, len(fos))
	for _, fo := range fos {
		d := fo.Decl
		ops = append(ops, OpIO{
			OpID: d.ID, Label: d.Label, Desc: d.Desc, Stream: d.Stream,
			InType: fo.TypeName + "In", OutType: fo.TypeName + "Out",
			Inputs: d.Inputs, Outputs: d.Outputs,
		})
	}

	named, err := namedStructs(m)
	if err != nil {
		return nil, err
	}

	out := GoFromManifest{}
	types, err := RenderTypesNamed(pkg, SchemaRef{}, ops, named)
	if err != nil {
		return nil, err
	}
	out["zz_types.go"] = types

	reg, err := renderGoRegister(pkg, m, fos, doc)
	if err != nil {
		return nil, err
	}
	out["zz_register.go"] = reg

	if m.Credential != nil && len(m.Credential.Fields) > 0 {
		cred, cerr := RenderCredential(pkg, SchemaRef{}, m.Credential.Fields)
		if cerr != nil {
			return nil, cerr
		}
		out["zz_credential.go"] = cred
	}
	// The auth flow reuses the schema path's renderer outright: it declares contract.AuthMeta and
	// hands the handlers to the host, and none of that ever referenced a schema package. The SDK
	// then contributes the reserved auth.* operations at handshake time, exactly as it does for a
	// schema-declared plugin.
	if m.Credential != nil && m.Credential.Auth != nil {
		a := m.Credential.Auth
		au, aerr := RenderAuth(pkg, AuthMeta{Kind: a.Kind, Steps: a.Steps(), Provider: a.Provider, Scopes: a.Scopes})
		if aerr != nil {
			return nil, aerr
		}
		out["zz_auth.go"] = au
	}
	if len(m.Events) > 0 {
		ev, eerr := renderGoEvents(pkg, m)
		if eerr != nil {
			return nil, eerr
		}
		out["zz_events.go"] = ev
	}
	return out, nil
}

// namedStructs collects every structure the manifest names with goType, so the generated file can
// declare it. Two different shapes under one name is a manifest bug worth naming: silently picking
// one would give half the operations a struct whose fields do not match their contract.
func namedStructs(m *Manifest) ([]NamedStruct, error) {
	byName := map[string][]Field{}
	var walk func(fs []Field) error
	walk = func(fs []Field) error {
		for _, f := range fs {
			if f.GoType != "" && len(f.Fields) > 0 && !isIntGoType(f.GoType) {
				if prev, ok := byName[f.GoType]; ok {
					if !sameFieldShape(prev, f.Fields) {
						return fmt.Errorf("goType %q is used for two different structures — give one of them another name", f.GoType)
					}
				} else {
					byName[f.GoType] = f.Fields
				}
			}
			if err := walk(f.Fields); err != nil {
				return err
			}
			if f.ValueType != nil {
				if err := walk([]Field{*f.ValueType}); err != nil {
					return err
				}
			}
			// A oneOf branch names its own type (the generated accessor returns *DocObject /
			// []Block), so those structures need declaring too — missing them compiles to a
			// reference to a type nothing defines.
			for _, v := range f.OneOf {
				gt := v.GoType
				if gt == "" {
					gt = v.Name
				}
				if gt != "" && len(v.Fields) > 0 {
					if prev, ok := byName[gt]; ok {
						if !sameFieldShape(prev, v.Fields) {
							return fmt.Errorf("goType %q is used for two different structures — give one of them another name", gt)
						}
					} else {
						byName[gt] = v.Fields
					}
				}
				if err := walk(v.Fields); err != nil {
					return err
				}
			}
		}
		return nil
	}
	for _, fo := range m.AllOperations() {
		if err := walk(fo.Decl.Inputs); err != nil {
			return nil, err
		}
		if err := walk(fo.Decl.Outputs); err != nil {
			return nil, err
		}
	}
	for _, e := range m.Events {
		if err := walk(e.Fields); err != nil {
			return nil, err
		}
	}
	if m.Credential != nil {
		if err := walk(m.Credential.Fields); err != nil {
			return nil, err
		}
	}
	out := make([]NamedStruct, 0, len(byName))
	for n, fs := range byName {
		out = append(out, NamedStruct{Name: n, Fields: fs})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out, nil
}

func sameFieldShape(a, b []Field) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i].Name != b[i].Name || a[i].Type != b[i].Type {
			return false
		}
	}
	return true
}

// renderGoRegister renders the operation contracts and one OnXxx per operation.
//
// The contract is a literal rather than a call into a schema package: the manifest is the source, so
// the values belong in the generated file where a reviewer can see exactly what will be reported.
func renderGoRegister(pkg string, m *Manifest, fos []FlatOp, doc string) (string, error) {
	var b strings.Builder
	b.WriteString("// Code generated by sokel-gen. DO NOT EDIT.\n//\n")
	b.WriteString("// Declared in manifest.yml. Change an operation there and regenerate.\n//\n")
	b.WriteString("// Registration goes through plugin.Host, which is transport-agnostic: sokel (NATS) and the\n")
	b.WriteString("// platform's in-process host each implement it, so the same handler runs under either.\n\n")
	b.WriteString("package " + pkg + "\n\n")
	b.WriteString("import (\n\t\"encoding/json\"\n\n\t\"github.com/sokel-dev/sokel-plugin-sdk/contract\"\n\t\"github.com/sokel-dev/sokel-plugin-sdk/plugin\"\n)\n\n")

	sorted := append([]FlatOp(nil), fos...)
	sort.Slice(sorted, func(i, j int) bool { return sorted[i].Decl.ID < sorted[j].Decl.ID })

	if doc != "" {
		b.WriteString("// Doc is this plugin's user guide, inlined from the file the manifest points at.\n")
		b.WriteString("// RegisterDoc hands it to hosts that can render it.\n")
		b.WriteString("const Doc = " + strconv.Quote(doc) + "\n\n")
		b.WriteString("// RegisterDoc gives the host this plugin's guide. Hosts that cannot render one ignore it.\n")
		b.WriteString("func RegisterDoc(h plugin.Host) { plugin.DeclareDoc(h, Doc, \"\") }\n\n")
	}
	if len(m.Capabilities) > 0 {
		caps := make([]string, 0, len(m.Capabilities))
		for k := range m.Capabilities {
			caps = append(caps, k)
		}
		sort.Strings(caps)
		b.WriteString("// RegisterCapabilities self-reports how far the optional capabilities go.\n")
		b.WriteString("func RegisterCapabilities(h plugin.Host) {\n\tplugin.DeclareCapabilities(h, map[string]bool{\n")
		for _, k := range caps {
			fmt.Fprintf(&b, "\t\t%s: %t,\n", strconv.Quote(k), m.Capabilities[k])
		}
		b.WriteString("\t})\n}\n\n")
	}

	for _, fo := range sorted {
		d, name := fo.Decl, fo.TypeName
		fmt.Fprintf(&b, "// %sOperation is the contract of %q as declared in manifest.yml.\n", name, d.ID)
		fmt.Fprintf(&b, "func %sOperation() contract.Operation {\n\treturn contract.Operation{\n", name)
		fmt.Fprintf(&b, "\t\tID: %s,\n", strconv.Quote(d.ID))
		fmt.Fprintf(&b, "\t\tLabel: %s,\n", strconv.Quote(orDefaultStr(d.Label, d.ID)))
		if d.Desc != "" {
			fmt.Fprintf(&b, "\t\tDesc: %s,\n", strconv.Quote(d.Desc))
		}
		if d.Stream {
			b.WriteString("\t\tStream: true,\n")
		}
		if d.Internal {
			b.WriteString("\t\tInternal: true,\n")
		}
		if d.TimeoutSec > 0 {
			fmt.Fprintf(&b, "\t\tTimeoutSec: %d,\n", d.TimeoutSec)
		}
		if fo.Capability != "" {
			fmt.Fprintf(&b, "\t\tCapability: %s,\n", strconv.Quote(fo.Capability))
		}
		fmt.Fprintf(&b, "\t\tInputs: %s,\n", renderFieldsEmpty(d.Inputs, 2))
		fmt.Fprintf(&b, "\t\tOutputs: %s,\n", renderFieldsEmpty(d.Outputs, 2))
		b.WriteString("\t}\n}\n\n")

		fmt.Fprintf(&b, "// On%s registers the implementation of %q.\n", name, d.ID)
		if d.Stream {
			fmt.Fprintf(&b, "func On%s(h plugin.Host, fn func(plugin.Ctx, *%sIn, plugin.Sink) error) {\n", name, name)
			fmt.Fprintf(&b, "\th.Register(%sOperation(), func(ctx plugin.Ctx, raw json.RawMessage, out plugin.Sink) error {\n", name)
			fmt.Fprintf(&b, "\t\tvar in %sIn\n\t\tif err := contract.BindInput(raw, &in); err != nil {\n\t\t\treturn err\n\t\t}\n", name)
			b.WriteString("\t\treturn fn(ctx, &in, out)\n\t})\n}\n\n")
			continue
		}
		fmt.Fprintf(&b, "func On%s(h plugin.Host, fn func(plugin.Ctx, *%sIn) (*%sOut, error)) {\n", name, name, name)
		fmt.Fprintf(&b, "\th.Register(%sOperation(), func(ctx plugin.Ctx, raw json.RawMessage, out plugin.Sink) error {\n", name)
		fmt.Fprintf(&b, "\t\tvar in %sIn\n\t\tif err := contract.BindInput(raw, &in); err != nil {\n\t\t\treturn err\n\t\t}\n", name)
		b.WriteString("\t\tres, err := fn(ctx, &in)\n\t\tif err != nil {\n\t\t\treturn err\n\t\t}\n\t\tif res != nil {\n\t\t\tout.Vars(res)\n\t\t}\n\t\treturn nil\n\t})\n}\n\n")
	}

	src, err := format.Source([]byte(b.String()))
	if err != nil {
		return "", fmt.Errorf("the generated code will not format (most likely a rendering bug): %w\n---\n%s", err, b.String())
	}
	return string(src), nil
}

// renderGoEvents renders event payload types, the declaration function and the typed triggers.
//
// It cannot reuse the schema path's renderer: that one declares each event as
// contract.EventOf(&XEvent{}), which requires the payload type to implement EventSchema — a method
// the schema package supplies and a manifest has no way to produce. Here the contract is a literal,
// the same way operations are.
func renderGoEvents(pkg string, m *Manifest) (string, error) {
	// **Declaration order, not sorted.** Operations are sorted (the schema path reads them out of a
	// package, where source order means nothing), but events keep the order the manifest lists them
	// in — that is what the Python and TypeScript shells report, and the three have to agree.
	events := append([]EventDecl(nil), m.Events...)

	need := &imports{}
	var body strings.Builder
	for _, e := range events {
		body.WriteString(renderEventPayload(
			EventIO{ID: e.ID, Label: e.Label, Desc: e.Desc, TypeName: exportName(e.ID) + "Event", Fields: e.Fields},
			"", need))
	}

	var b strings.Builder
	b.WriteString("// Code generated by sokel-gen. DO NOT EDIT.\n")
	b.WriteString("//\n// Event payload types and trigger functions are declared in manifest.yml.\n")
	b.WriteString("// Change an event there, then regenerate.\n\n")
	b.WriteString("package " + pkg + "\n\n")
	b.WriteString("import (\n")
	if need.json {
		b.WriteString("\t\"encoding/json\"\n\n")
	}
	b.WriteString("\t\"github.com/sokel-dev/sokel-plugin-sdk/contract\"\n\t\"github.com/sokel-dev/sokel-plugin-sdk/plugin\"\n)\n\n")
	b.WriteString(body.String())

	b.WriteString("// DeclareEvents hands this plugin's event contracts to the host.\n")
	b.WriteString("func DeclareEvents(h plugin.EventHost) {\n\tevents := []contract.Event{\n")
	for _, e := range events {
		desc := ""
		if e.Desc != "" {
			desc = "Desc: " + strconv.Quote(e.Desc) + ", "
		}
		fmt.Fprintf(&b, "\t\t{ID: %s, Label: %s, %sFields: %s},\n",
			strconv.Quote(e.ID), strconv.Quote(orDefaultStr(e.Label, e.ID)), desc, renderFields(e.Fields, 3, "contract"))
	}
	b.WriteString("\t}\n\tfor _, e := range events {\n\t\th.DeclareEvent(e)\n\t}\n")
	if len(m.EventsCommon) > 0 {
		b.WriteString("\tnames := []string{")
		for i, n := range m.EventsCommon {
			if i > 0 {
				b.WriteString(", ")
			}
			b.WriteString(strconv.Quote(n))
		}
		b.WriteString("}\n")
		// Consistency was checked when the manifest was parsed (every common field must exist in every
		// event with the same type), so this cannot fail here.
		b.WriteString("\tfields, _ := contract.ValidateCommonFields(events, names)\n")
		b.WriteString("\th.DeclareEventsCommon(fields, names)\n")
	}
	b.WriteString("}\n\n")

	for _, e := range events {
		name := exportName(e.ID)
		fmt.Fprintf(&b, "// Trigger%s pushes one %q event.\n", name, orDefaultStr(e.Label, e.ID))
		b.WriteString("// eventID is the platform's deduplication key: re-pushing the same message triggers once.\n")
		fmt.Fprintf(&b, "func Trigger%s(ctx plugin.SourceCtx, eventID string, p *%sEvent) error {\n", name, name)
		fmt.Fprintf(&b, "\treturn ctx.Trigger(%s, eventID, p)\n}\n\n", strconv.Quote(e.ID))
	}

	out, err := format.Source([]byte(b.String()))
	if err != nil {
		return "", fmt.Errorf("the generated event code will not format (most likely a rendering bug): %w\n---\n%s", err, b.String())
	}
	return string(out), nil
}

// renderFieldsEmpty renders an empty field list as an empty slice rather than nil.
//
// An operation that takes no input must report "inputs": [] and not null: everything downstream —
// the platform, the panel, the canvas — would otherwise have to guard against null, and one of them
// eventually will not. The other renderers pass through nonNil for the same reason.
func renderFieldsEmpty(fs []Field, indent int) string {
	if len(fs) == 0 {
		return "[]contract.Field{}"
	}
	return renderFields(fs, indent, "contract")
}
