// Copyright 2026 The Sokel Authors
// SPDX-License-Identifier: Apache-2.0

package main

// `sokel-gen docs` / `sokel-gen example` print everything needed to write a plugin to stdout.
//
// The reader is often not a human. An LLM writing a plugin can run commands and read stdout, but
// may have neither GitHub access nor a checkout of this repository — so the format guide, the JSON
// Schema and a reference declaration covering every shape are embedded in the binary (see embed.go
// at the repository root), one command away.

import (
	"fmt"
	"os"
	"strings"

	sdk "github.com/sokel-dev/sokel-plugin-sdk"
)

// runDocs prints the format guide. `docs example` forwards to runExample so that either guess
// works: a tool where guessing wrong means going back to read help is exactly where an agent stalls.
func runDocs(args []string) error {
	topic := ""
	if len(args) > 0 {
		topic = args[0]
	}
	switch topic {
	case "", "manifest", "format":
		fmt.Print(sdk.ManifestDoc)
	case "schema":
		fmt.Println(strings.TrimRight(sdk.Schema, "\n"))
	case "example":
		return runExample(args[1:])
	case "list":
		fmt.Print(docsTopics)
	default:
		return fmt.Errorf("unknown topic %q — try manifest (default) / schema / example, or `sokel-gen docs list`", topic)
	}
	return nil
}

// runExample prints the reference plugin: one declaration plus both implementations — the very
// files that run in this repository, not a trimmed-down copy.
func runExample(args []string) error {
	which := ""
	if len(args) > 0 {
		which = args[0]
	}
	switch which {
	case "", "yaml", "manifest":
		fmt.Print(exampleBanner)
		fmt.Print(sdk.ExampleManifest)
	case "python", "py":
		fmt.Print(sdk.ExamplePython)
	case "ts", "node", "typescript":
		fmt.Print(sdk.ExampleNode)
	case "go":
		// A Go contract is not written in sokel.yaml; pointing there beats printing a half-truth
		return fmt.Errorf("Go plugins declare their contract in a schema/ package, not in sokel.yaml — `sokel-gen init ./my-plugin` scaffolds exactly that shape")
	default:
		return fmt.Errorf("unknown implementation %q — try yaml (default) / python / node", which)
	}
	return nil
}

const exampleBanner = `# Below is the full declaration of the kitchen-sink reference plugin: every field shape,
# files, streaming, events, webhooks and collaborative auth, once each.
# When copying it, change plugin.name and the codegen.out paths.
#
# Implementations: sokel-gen example python / sokel-gen example node
# Format guide:    sokel-gen docs          JSON Schema: sokel-gen docs schema
`

const docsTopics = `sokel-gen docs [topic]

  manifest   how to write sokel.yaml (default) — field types, oneOf/valueType/opaque,
             events and common fields, credentials and auth flows, generating and checking
  schema     the JSON Schema: editor completion, or feed it to any schema-aware tool
  example    the reference declaration covering every shape (= sokel-gen example)

sokel-gen example [lang]

  yaml       the reference plugin's declaration (default)
  python     the matching Python implementation
  node       the matching TypeScript implementation
`

// agentHint is the opening line for the "let an agent write the plugin" path: init to scaffold,
// docs to learn the format, example to copy from, generate to build and validate. Four commands are
// enough to take a plugin from nothing to running.
func agentHint(w *os.File) {
	fmt.Fprint(w, `
Pointing an agent at these four is enough for it to write a plugin:
  sokel-gen docs                        # how to write sokel.yaml (the full format guide)
  sokel-gen example                     # a real declaration using every shape, to copy and edit
  sokel-gen init -lang python|ts <dir>  # scaffold (schema annotation and both docs included)
  sokel-gen generate <dir>              # build the typed shell; reports every problem at once
`)
}
