// Copyright 2026 The Sokel Authors
// SPDX-License-Identifier: Apache-2.0

package sokelgen

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// ImportPathOf infers a directory's Go import path: walk up to go.mod for the module, then append the
// relative path.
//
// Not go list: that shells out and is slow, whereas all that is needed here is a string, which go.mod
// supplies. It also keeps the generator working without a complete build environment (the step that
// genuinely needs one is running the schema later).
func ImportPathOf(dir string) (string, error) {
	abs, err := filepath.Abs(dir)
	if err != nil {
		return "", err
	}
	root := abs
	for {
		if b, err := os.ReadFile(filepath.Join(root, "go.mod")); err == nil {
			mod := moduleName(string(b))
			if mod == "" {
				return "", fmt.Errorf("no module declaration found in %s/go.mod", root)
			}
			rel, err := filepath.Rel(root, abs)
			if err != nil {
				return "", err
			}
			if rel == "." {
				return mod, nil
			}
			return mod + "/" + filepath.ToSlash(rel), nil
		}
		parent := filepath.Dir(root)
		if parent == root {
			return "", fmt.Errorf("no go.mod found walking up from %s", dir)
		}
		root = parent
	}
}

func moduleName(gomod string) string {
	for _, line := range strings.Split(gomod, "\n") {
		line = strings.TrimSpace(line)
		if rest, ok := strings.CutPrefix(line, "module "); ok {
			return strings.TrimSpace(rest)
		}
	}
	return ""
}
