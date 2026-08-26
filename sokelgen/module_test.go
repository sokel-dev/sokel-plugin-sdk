// Copyright 2026 The Sokel Authors
// SPDX-License-Identifier: Apache-2.0

package sokelgen

import (
	"strings"
	"testing"
)

func TestImportPathOf(t *testing.T) {
	// This package itself: the module plus a relative path
	got, err := ImportPathOf(".")
	if err != nil {
		t.Fatal(err)
	}
	if got != "github.com/sokel-dev/sokel-plugin-sdk/sokelgen" {
		t.Errorf("this package's import path: %q", got)
	}
	// A subdirectory
	sub, err := ImportPathOf("internal/demoschema")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasSuffix(sub, "/sokelgen/internal/demoschema") {
		t.Errorf("the subpackage's import path: %q", sub)
	}
	// No go.mod must give a readable error rather than a path assembled out of guesswork
	if _, err := ImportPathOf("/"); err == nil {
		t.Error("the root has no go.mod and should fail")
	}
}
