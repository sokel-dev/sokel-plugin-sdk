// Copyright 2026 The Sokel Authors
// SPDX-License-Identifier: Apache-2.0

package main

// Generating and checking the manifest entry point (sokel.yaml / sokel.json).
//
// Its relationship to the schema/ entry point: **the same contract, declared the way each language
// prefers**. A Go plugin writes the contract as Go code (compile-time checks, loops and constants
// allowed); Python and Node plugins write YAML, so declaring a few fields does not begin with
// installing a Go toolchain to read a builder API.

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/sokel-dev/sokel-plugin-sdk/sokelgen"
)

// defaultOut is the generated file name per language. Fixed rather than configurable: once the
// names are uniform, reading someone else's plugin does not start with "which file is generated?".
var defaultOut = map[string]string{
	"ts":     "sokel.gen.ts",
	"python": "sokel_gen.py",
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
			fmt.Printf("sokel-gen: generated %s (%d operations, %d events)\n", out, len(m.Operations), len(m.Events))
		}
	}
	if len(stale) > 0 {
		return fmt.Errorf("%s", strings.Join(stale, "；"))
	}
	if check && !quiet {
		fmt.Printf("sokel-gen: %s is up to date (%d operations)\n", filepath.Base(manifestPath), len(m.Operations))
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
		// A Go contract is declared in a schema/ package: that path expresses things a manifest cannot
		// (reusing existing Go types, oneOf pointing at real types), not the other way round.
		return "", fmt.Errorf("Go plugins declare their contract in a schema/ package (what sokel-gen init scaffolds); manifests generate ts / python")
	}
	return "", fmt.Errorf("unknown language %q (ts / python)", lang)
}
