// Copyright 2026 The Sokel Authors
// SPDX-License-Identifier: Apache-2.0

// Package sokelgen is the stable, importable part of the sokel-gen toolchain: reading a plugin's
// manifest.yml, exporting it as JSON and building a registry index entry from it (what a platform or a
// registry needs), and rendering a kernel's TypeScript contract.
//
// Everything else sokel-gen does — scanning Go declarations, rendering Go / Python / TypeScript plugin
// code, auditing — lives in internal/gen and may change in any release. It used to be exported from
// here too (some ninety symbols, of which a platform used about ten), which made every refactor of the
// generator a breaking change for whoever had imported one of them.
package sokelgen

import gen "github.com/sokel-dev/sokel-plugin-sdk/internal/gen"

// Manifest is a parsed manifest.yml.
type Manifest = gen.Manifest

// IndexEntry is one plugin's entry in a registry index; IndexEntryI18n its localized fields.
type (
	IndexEntry     = gen.IndexEntry
	IndexEntryI18n = gen.IndexEntryI18n
)

// IndexVersion is the index format version BuildIndexEntry produces.
const IndexVersion = gen.IndexVersion

// Package is a loaded Go package of contract declarations; OpIO one operation's contract.
type (
	Package = gen.Package
	OpIO    = gen.OpIO
)

// ParseManifest parses manifest text (YAML, or JSON when asJSON) and validates it.
func ParseManifest(raw []byte, asJSON bool) (*Manifest, error) { return gen.ParseManifest(raw, asJSON) }

// LoadManifest reads and parses a manifest file.
func LoadManifest(path string) (*Manifest, error) { return gen.LoadManifest(path) }

// FindManifest returns the path of the manifest in dir.
func FindManifest(dir string) (string, error) { return gen.FindManifest(dir) }

// ExportManifestJSON renders a manifest as JSON, with the user-facing doc attached.
func ExportManifestJSON(m *Manifest, doc string) ([]byte, error) {
	return gen.ExportManifestJSON(m, doc)
}

// BuildIndexEntry builds a registry index entry from a manifest.
func BuildIndexEntry(m *Manifest, doc string) (*IndexEntry, error) {
	return gen.BuildIndexEntry(m, doc)
}

// ValidVersionExpr reports whether s is a valid version constraint expression.
func ValidVersionExpr(s string) bool { return gen.ValidVersionExpr(s) }

// ValidateIconFile checks a plugin icon file (SVG or PNG: small, about square, nothing that runs or reaches outside
// the file). Loading a manifest from disk already checks the icon it names.
func ValidateIconFile(path string) error { return gen.ValidateIconFile(path) }

// Icon file limits (see ValidateIconFile).
const (
	MaxIconSVGBytes = gen.MaxIconSVGBytes
	MaxIconPNGBytes = gen.MaxIconPNGBytes
	MinIconPNGSize  = gen.MinIconPNGSize
)

// IconIsBrand reports whether a plugin.icon value names a built-in brand mark (brand:<id>).
func IconIsBrand(icon string) bool { return gen.IconIsBrand(icon) }

// LoadDir loads the Go package of contract declarations in dir.
func LoadDir(dir string) (*Package, error) { return gen.LoadDir(dir) }

// ImportPathOf resolves dir's Go import path from the enclosing go.mod.
func ImportPathOf(dir string) (string, error) { return gen.ImportPathOf(dir) }

// LoadDeclarations evaluates the named contract types of the package at importPath.
func LoadDeclarations(dir, importPath string, types []string) ([]OpIO, error) {
	return gen.LoadDeclarations(dir, importPath, types)
}

// RenderTS renders operations' contracts as TypeScript types.
func RenderTS(pkgLabel string, ops []OpIO) (string, error) { return gen.RenderTS(pkgLabel, ops) }
