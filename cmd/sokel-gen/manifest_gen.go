// Copyright 2026 The Sokel Authors
// SPDX-License-Identifier: Apache-2.0

package main

// Generating and checking the manifest entry point (manifest.yml / sokel.json).
//
// Its relationship to the schema/ entry point: **the same contract, declared the way each language
// prefers**. A Go plugin writes the contract as Go code (compile-time checks, loops and constants
// allowed); Python and Node plugins write YAML, so declaring a few fields does not begin with
// installing a Go toolchain to read a builder API.

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/sokel-dev/sokel-plugin-sdk/sokelgen"
)

// defaultOut is the generated file name per language. Fixed rather than configurable: once the
// names are uniform, reading someone else's plugin does not start with "which file is generated?".
var defaultOut = map[string]string{
	"ts":     "sokel.gen.ts",
	"python": "sokel_gen.py",
}

// goPkgName is the package the generated Go files join.
//
// The main package may not exist yet — a new plugin's main.go wants the generated types, and
// generation wants the package name — so default to main rather than making the author write a dummy
// file first. Same chicken-and-egg the schema path resolves the same way.
func goPkgName(dir string) string {
	if pkg, err := sokelgen.LoadDir(dir); err == nil && pkg.Name != "" {
		return pkg.Name
	}
	return "main"
}

// writeOrCheck writes each generated file, or in check mode reports the stale ones without writing.
func writeOrCheck(dir string, files map[string]string, check bool) ([]string, error) {
	names := make([]string, 0, len(files))
	for n := range files {
		names = append(names, n)
	}
	sort.Strings(names)
	var stale []string
	for _, name := range names {
		path := filepath.Join(dir, name)
		if check {
			old, rerr := os.ReadFile(path)
			switch {
			case rerr != nil:
				stale = append(stale, name+" does not exist (changed manifest.yml without generating?)")
			case string(old) != files[name]:
				stale = append(stale, name+" is stale")
			}
			continue
		}
		if err := os.WriteFile(path, []byte(files[name]), 0o644); err != nil {
			return nil, fmt.Errorf("writing %s: %w", path, err)
		}
	}
	return stale, nil
}

// generateManifest generates (or checks) the output of one manifest-declared plugin.
func generateManifest(manifestPath string, check, quiet bool, langFlag string) error {
	m, err := sokelgen.LoadManifest(manifestPath)
	if err != nil {
		return err
	}
	doc, err := m.DocMarkdown()
	if err != nil {
		return err
	}
	targets := m.Codegen
	if langFlag != "" {
		// With -lang, generate only that one; use the manifest's out if it has one, else the default
		picked := sokelgen.CodegenList{{Lang: langFlag}}
		for _, t := range m.Codegen {
			if t.Lang == langFlag {
				picked = sokelgen.CodegenList{t}
			}
		}
		targets = picked
	}
	if len(targets) == 0 {
		return fmt.Errorf("%s does not say which language to generate — set lang: ts / python under codegen, or pass -lang", manifestPath)
	}

	var stale []string
	for _, t := range targets {
		// Go is several files (types / register / credential / events), like the schema path — the
		// point being that main.go looks the same whichever way the contract was declared.
		if t.Lang == "go" {
			// For Go, `out` names a **directory** rather than a file — the output is several files,
			// as it is for a schema-declared plugin. Empty means "next to the manifest".
			dir := m.Dir()
			if t.Out != "" {
				dir = filepath.Join(m.Dir(), t.Out)
				if err := os.MkdirAll(dir, 0o755); err != nil {
					return fmt.Errorf("creating directory %s: %w", dir, err)
				}
			}
			files, gerr := sokelgen.RenderGoFromManifest(m, doc, goPkgName(dir))
			if gerr != nil {
				return gerr
			}
			st, werr := writeOrCheck(dir, files, check)
			if werr != nil {
				return werr
			}
			stale = append(stale, st...)
			continue
		}
		src, rerr := renderManifest(m, doc, t.Lang)
		if rerr != nil {
			return rerr
		}
		out := t.Out
		if out == "" {
			out = defaultOut[t.Lang]
		}
		path := filepath.Join(m.Dir(), out)
		if check {
			// "changed the declaration, forgot to regenerate" is how codegen usually fails; CI stops it here
			old, ferr := os.ReadFile(path)
			switch {
			case ferr != nil:
				stale = append(stale, fmt.Sprintf("%s does not exist (changed %s without generating?)", out, filepath.Base(manifestPath)))
			case string(old) != src:
				stale = append(stale, out+" is stale")
			}
			continue
		}
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			return fmt.Errorf("creating directory %s: %w", filepath.Dir(path), err)
		}
		if err := os.WriteFile(path, []byte(src), 0o644); err != nil {
			return fmt.Errorf("writing %s: %w", path, err)
		}
		if !quiet {
			fmt.Printf("sokel-gen: generated %s (%d operations, %d events)\n", out, len(m.AllOperations()), len(m.Events))
		}
	}
	if len(stale) > 0 {
		return fmt.Errorf("%s", strings.Join(stale, "；"))
	}
	if check && !quiet {
		fmt.Printf("sokel-gen: %s is up to date (%d operations)\n", filepath.Base(manifestPath), len(m.AllOperations()))
	}
	return nil
}

func renderManifest(m *sokelgen.Manifest, doc, lang string) (string, error) {
	switch lang {
	case "ts":
		return sokelgen.RenderTSPlugin(m, doc)
	case "python":
		return sokelgen.RenderPythonPlugin(m, doc)
	case "go":
		// Go from a manifest is rendered as several files, so it does not go through this
		// single-source path — generateManifest handles it before getting here.
		return "", fmt.Errorf("internal: Go is rendered by renderManifestGo")
	}
	return "", fmt.Errorf("unknown language %q (go / ts / python)", lang)
}
