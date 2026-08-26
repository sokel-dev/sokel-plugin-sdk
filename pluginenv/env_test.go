// Copyright 2026 The Sokel Authors
// SPDX-License-Identifier: Apache-2.0

package pluginenv

import "testing"

// One place for the prefix: plugins should not each spell out their own "SOKEL_TOKEN" literal.
func TestGetReadsPrefixedName(t *testing.T) {
	t.Setenv("SOKEL_TOKEN", " v ")
	if got := Get("TOKEN"); got != "v" {
		t.Errorf("should read SOKEL_TOKEN and trim whitespace, got %q", got)
	}
}

// Only the SOKEL_ prefix is recognised; anything else reads as nothing, so no compatibility layer turns
// into a piece of history nobody removes.
func TestGetHasNoLegacyFallback(t *testing.T) {
	t.Setenv("PLUGIN_TOKEN", "legacy")
	t.Setenv("OTHER_TOKEN", "older")
	if got := Get("TOKEN"); got != "" {
		t.Errorf("the old prefix should no longer be recognised, got %q", got)
	}
}
