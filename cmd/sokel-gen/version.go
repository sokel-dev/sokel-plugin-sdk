// Copyright 2026 The Sokel Authors
// SPDX-License-Identifier: Apache-2.0

package main

import (
	"fmt"
	"os"
	"runtime/debug"
)

// version is stamped in at build time (-X main.version=v0.5.3) for the released archives.
//
// It has to be a variable rather than only reading build info: `go install` bakes the module version
// into the binary, but a cross-compiled archive built from a checkout has no module version to read —
// it would report "(devel)", which is exactly the binary whose version someone most needs to know.
var version = ""

// runVersion prints the toolchain version. Whoever downloaded a binary has no `go list` to fall back
// on, and "which sokel-gen is this" is the first question when generated output looks unfamiliar.
func runVersion() error {
	fmt.Fprintln(os.Stdout, "sokel-gen "+versionString())
	return nil
}

func versionString() string {
	if version != "" {
		return version
	}
	if bi, ok := debug.ReadBuildInfo(); ok && bi.Main.Version != "" && bi.Main.Version != "(devel)" {
		return bi.Main.Version
	}
	return "(devel)"
}
