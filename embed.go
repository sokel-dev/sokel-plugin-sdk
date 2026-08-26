// Copyright 2026 The Sokel Authors
// SPDX-License-Identifier: Apache-2.0

// Package sdk does one thing: compile everything needed to write a plugin into the sokel-gen binary.
//
// Why embed at all: a sokel-gen installed with `go install` has no copy of this repository to hand. And
// whoever needs to read these is often not a person — an AI writing a plugin can run a command and read
// its stdout, but may well have no access to GitHub. `sokel-gen docs` and `sokel-gen example` are the
// entry points prepared for exactly that.
//
// This **references rather than copies**: the document, the schema and the reference declaration are
// still the repository's own, and what goes into the binary is that same file — a copy would eventually
// diverge, and whoever read the stale one would never know.
package sdk

import _ "embed"

// ManifestDoc is the guide to writing sokel.yaml (docs/manifest.md).
//
//go:embed docs/manifest.md
var ManifestDoc string

// Schema is sokel.yaml's JSON Schema, for editor completion and for any tool that reads schemas.
//
//go:embed docs/sokel.schema.json
var Schema string

// ExampleManifest is the reference declaration covering every contract shape
// (examples/kitchen-sink/sokel.yaml).
//
//go:embed examples/kitchen-sink/sokel.yaml
var ExampleManifest string

// ExamplePython is the Python implementation of that declaration.
//
//go:embed examples/kitchen-sink/python/main.py
var ExamplePython string

// ExampleNode is the TypeScript implementation of that declaration.
//
//go:embed examples/kitchen-sink/node/src/main.ts
var ExampleNode string
