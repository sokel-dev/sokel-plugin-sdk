// Copyright 2026 The Sokel Authors
// SPDX-License-Identifier: Apache-2.0

// Package pluginenv is one place to read a plugin's environment variables.
//
// It does one thing: keep the SOKEL_ prefix in a single place instead of letting every plugin spell out
// its own literals.
//
// There is **no** compatibility layer for other prefixes: accepting a second one would save a single
// redeployment and buy a piece of history nobody dares remove — a bad trade.
package pluginenv

import (
	"os"
	"strings"
)

const prefix = "SOKEL_"

// Get reads SOKEL_<name>. name carries no prefix: Get("TOKEN"), Get("NATS_TOKEN").
func Get(name string) string { return strings.TrimSpace(os.Getenv(prefix + name)) }
