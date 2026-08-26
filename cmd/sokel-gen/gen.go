// Copyright 2026 The Sokel Authors
// SPDX-License-Identifier: Apache-2.0

package main

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/sokel-dev/sokel-plugin-sdk/sokelgen"
)

// loaded is everything read out of one plugin's declaration. generate and export share this single
// load path, so the two cannot end up with different ideas of what counts as an operation.
type loaded struct {
	pkg         *sokelgen.Package
	ops         []sokelgen.OpIO
	importPath  string
	schemaDir   string
	credTypes   []string
	eventTypes  []string
	commonTypes []string
}

func load(dir, schemaSub string) (*loaded, error) {
	schemaDir := filepath.Join(dir, schemaSub)
	pkg, err := sokelgen.LoadDir(schemaDir)
	if err != nil {
		return nil, err
	}
	types, err := pkg.SchemaOps()
	if err != nil {
		return nil, err
	}
	importPath, err := sokelgen.ImportPathOf(schemaDir)
	if err != nil {
		return nil, err
	}
	ops, err := sokelgen.LoadDeclarations(schemaDir, importPath, types)
	if err != nil {
		return nil, err
	}
	return &loaded{
		pkg: pkg, ops: ops, importPath: importPath, schemaDir: schemaDir,
		credTypes:  pkg.CredentialTypes(),
		eventTypes: pkg.EventTypes(), commonTypes: pkg.CommonFieldsTypes(),
	}, nil
}

// warn runs two quality audits over the declaration.
//   - Weak typing: every opaque should be a deliberate decision, not the lazy default.
//   - Array element shape: omitting it is **silent** (field.Array takes any as its shape argument, so
//     passing a description compiles), and downstream all anyone sees is an opaque array.
//
// With several plugins the directory prefix is added, or a pile of warnings says nothing about whose
// they are.
func (l *loaded) warn(dir string, prefixed bool) {
	for _, w := range []string{
		sokelgen.FormatOpaqueWarnings(sokelgen.AuditOpaque(l.ops)),
		sokelgen.FormatArrayWarnings(sokelgen.AuditArrays(l.ops)),
	} {
		if w == "" {
			continue
		}
		if prefixed {
			for _, line := range strings.Split(strings.TrimRight(w, "\n"), "\n") {
				fmt.Fprintf(os.Stderr, "%s: %s\n", dir, line)
			}
			continue
		}
		fmt.Fprint(os.Stderr, w)
	}
}

// manifest assembles this plugin's Go declaration into a language-neutral manifest (for export yaml).
// Credentials, events and authentication all come along: exporting only the operations would leave
// anyone reimplementing it in another language with half a plugin.
func (l *loaded) manifest(dir string) (*sokelgen.Manifest, error) {
	var cred []sokelgen.Field
	if len(l.credTypes) > 0 {
		var err error
		if cred, err = sokelgen.LoadCredential(l.schemaDir, l.importPath, l.credTypes); err != nil {
			return nil, err
		}
	}
	var auth *sokelgen.AuthMeta
	if authTypes := l.pkg.AuthTypes(); len(authTypes) > 0 {
		var err error
		if auth, err = sokelgen.LoadAuth(l.schemaDir, l.importPath, authTypes); err != nil {
			return nil, err
		}
	}
	var events []sokelgen.EventIO
	var common []string
	if len(l.eventTypes) > 0 {
		var err error
		if events, common, err = sokelgen.LoadEvents(l.schemaDir, l.importPath, l.eventTypes, l.commonTypes); err != nil {
			return nil, err
		}
	}
	return sokelgen.ManifestFrom(filepath.Base(mustAbs(dir)), l.ops, cred, auth, events, common), nil
}

func mustAbs(dir string) string {
	abs, err := filepath.Abs(dir)
	if err != nil {
		return dir
	}
	return abs
}

// generateAny dispatches on the declaration entry point. schema/ wins when a directory has both: a
// Go plugin that also ships a sokel.yaml has usually exported it for someone else to read, and the
// generated output should still come from the Go path.
func generateAny(dir, schemaSub string, check, quiet bool, lang string) error {
	if fi, err := os.Stat(filepath.Join(dir, schemaSub)); err == nil && fi.IsDir() {
		return generateOne(dir, schemaSub, check, quiet)
	}
	mf, err := sokelgen.FindManifest(dir)
	if err != nil {
		return err
	}
	if mf == "" {
		return fmt.Errorf("%s has neither a %s/ directory nor a sokel.yaml", dir, schemaSub)
	}
	return generateManifest(mf, check, quiet, lang)
}

// generateOne generates (or checks) one plugin's zz_*.go files.
func generateOne(dir, schemaSub string, check, quiet bool) error {
	l, err := load(dir, schemaSub)
	if err != nil {
		return err
	}
	l.warn(dir, quiet)

	// The main package may not exist yet: a new plugin's main.go wants the generated types, and
	// generation wants the main package's name. Default to "main" for that chicken-and-egg case rather
	// than making the author write a dummy file just to run the generator.
	pkgName := "main"
	if mainPkg, err := sokelgen.LoadDir(dir); err == nil && mainPkg.Name != "" {
		pkgName = mainPkg.Name
	}
	sch := sokelgen.SchemaRef{Import: l.importPath, Name: l.pkg.Name}
	// Declaration and generated output in the **same package** (-schema .): that is the shape used
	// when a contract is declared next to its implementation. A package cannot import itself, and the
	// type names must not carry a package prefix.
	if filepath.Clean(schemaSub) == "." {
		pkgName = l.pkg.Name
		sch = sokelgen.SchemaRef{}
	}

	files := map[string]func() (string, error){
		"zz_types.go":    func() (string, error) { return sokelgen.RenderTypes(pkgName, sch, l.ops) },
		"zz_register.go": func() (string, error) { return sokelgen.RenderRegister(pkgName, sch, l.ops) },
	}
	// Only present when a credential contract is declared. A plugin with simple fields can keep using
	// a struct in package main with sokel.WithCredential[T]; move to a schema declaration when you need
	// enum candidates or defaults.
	if len(l.credTypes) > 0 {
		credFields, cerr := sokelgen.LoadCredential(l.schemaDir, l.importPath, l.credTypes)
		if cerr != nil {
			return cerr
		}
		files["zz_credential.go"] = func() (string, error) { return sokelgen.RenderCredential(pkgName, sch, credFields) }
	}
	// How the credential is obtained: generated only when declared.
	if authTypes := l.pkg.AuthTypes(); len(authTypes) > 0 {
		meta, aerr := sokelgen.LoadAuth(l.schemaDir, l.importPath, authTypes)
		if aerr != nil {
			return aerr
		}
		files["zz_auth.go"] = func() (string, error) { return sokelgen.RenderAuth(pkgName, *meta) }
	}
	// Only event-source plugins get this one: generating an empty file would look like something is
	// missing.
	if len(l.eventTypes) > 0 {
		events, common, eerr := sokelgen.LoadEvents(l.schemaDir, l.importPath, l.eventTypes, l.commonTypes)
		if eerr != nil {
			return eerr
		}
		files["zz_events.go"] = func() (string, error) { return sokelgen.RenderEvents(pkgName, sch, events, common) }
	}

	names := make([]string, 0, len(files))
	for name := range files {
		names = append(names, name)
	}
	sort.Strings(names)

	for _, name := range names {
		src, err := files[name]()
		if err != nil {
			return err
		}
		path := filepath.Join(dir, name)
		if check {
			// "changed the source, forgot to regenerate" is how codegen usually fails; CI stops it here.
			old, rerr := os.ReadFile(path)
			if rerr != nil {
				return fmt.Errorf("%s does not exist", name)
			}
			if string(old) != src {
				return fmt.Errorf("%s is stale", name)
			}
			continue
		}
		if err := os.WriteFile(path, []byte(src), 0o644); err != nil {
			return fmt.Errorf("writing %s: %w", path, err)
		}
	}
	if quiet {
		return nil
	}
	verb := "generated"
	if check {
		verb = "up to date:"
	}
	fmt.Printf("sokel-gen: %s %s (%d operations)\n", verb, strings.Join(names, " / "), len(l.ops))
	return nil
}

// export renders the same IR into non-Go artifacts on stdout.
//
//	json    the contract itself, language-neutral — deliberately without Go type names, so a
//	        generator for another language has nothing to work around
//	ts      the execution-contract table the frontend checks its hand-written UI schema against
//	python  pydantic models
func export(dir, schemaSub, format string) error {
	// A manifest plugin: the declaration is already language-neutral, so render straight from it —
	// no Go code needs to exist first
	if mf, ferr := sokelgen.FindManifest(dir); ferr == nil && mf != "" {
		if _, serr := os.Stat(filepath.Join(dir, schemaSub)); serr != nil {
			return exportManifest(mf, format)
		}
	}
	l, err := load(dir, schemaSub)
	if err != nil {
		return err
	}
	l.warn(dir, false)
	switch format {
	case "json":
		b, err := sokelgen.ExportContract(l.ops)
		if err != nil {
			return err
		}
		fmt.Println(string(b))
	case "ts":
		src, err := sokelgen.RenderTS(l.importPath, l.ops)
		if err != nil {
			return err
		}
		fmt.Print(src)
	case "python":
		src, err := sokelgen.RenderPython(l.ops)
		if err != nil {
			return err
		}
		fmt.Print(src)
	case "yaml":
		// Go declaration -> language-neutral manifest: an author working in another language can copy
		// the contract without reading Go, and without the Go version becoming the de facto standard.
		m, merr := l.manifest(dir)
		if merr != nil {
			return merr
		}
		out, merr := sokelgen.RenderManifestYAML(m)
		if merr != nil {
			return merr
		}
		fmt.Print(out)
	}
	return nil
}

// exportManifest exports a manifest plugin: json is the contract itself, ts / python the typed shell.
func exportManifest(path, format string) error {
	m, err := sokelgen.LoadManifest(path)
	if err != nil {
		return err
	}
	doc, err := m.DocMarkdown()
	if err != nil {
		return err
	}
	switch format {
	case "json":
		b, jerr := sokelgen.ExportManifestJSON(m, doc)
		if jerr != nil {
			return jerr
		}
		fmt.Println(string(b))
	case "yaml":
		out, yerr := sokelgen.RenderManifestYAML(m)
		if yerr != nil {
			return yerr
		}
		fmt.Print(out)
	default:
		src, rerr := renderManifest(m, doc, format)
		if rerr != nil {
			return rerr
		}
		fmt.Print(src)
	}
	return nil
}

// migrate reverse-generates schema declaration code from an old struct+tag contract, printing it for
// review rather than writing it out: what comes back is a starting point, and a human still has to
// decide which fields deserve a declared structure and which need an Opaque reason spelled out.
func migrate(dir string) error {
	pkg, err := sokelgen.LoadDir(dir)
	if err != nil {
		return err
	}
	ops, err := pkg.Ops()
	if err != nil {
		return err
	}
	src, err := sokelgen.RenderSchema("schema", ops)
	if err != nil {
		return err
	}
	fmt.Print(src)
	return nil
}
