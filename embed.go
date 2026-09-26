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

import (
	_ "embed"
	"encoding/json"
)

// ManifestDoc is the guide to writing manifest.yml (docs/manifest.md).
//
//go:embed docs/manifest.md
var ManifestDoc string

// Schema is manifest.yml's JSON Schema, for editor completion and for any tool that reads schemas.
//
//go:embed docs/sokel.schema.json
var Schema string

// ExampleManifest is the reference declaration covering every contract shape
// (examples/kitchen-sink/manifest.yml).
//
//go:embed examples/kitchen-sink/manifest.yml
var ExampleManifest string

// ExamplePython is the Python implementation of that declaration.
//
//go:embed examples/kitchen-sink/python/main.py
var ExamplePython string

// ExampleNode is the TypeScript implementation of that declaration.
//
//go:embed examples/kitchen-sink/node/src/main.ts
var ExampleNode string

//go:embed sdk-node/package.json
var nodePackageJSON []byte

// Version is the SDK release this binary was built from (x.y.z, no leading v), read from
// sdk-node/package.json. That file is bumped with every tag (see RELEASING.md) and one tag releases
// all three SDKs, so it is the version of every one of them. `sokel-gen init` pins new plugins to it:
// a hand-written pin was left at 0.3 through two minors and new plugins installed an SDK the
// platform no longer accepted.
func Version() string {
	var pkg struct {
		Version string `json:"version"`
	}
	_ = json.Unmarshal(nodePackageJSON, &pkg)
	return pkg.Version
}
