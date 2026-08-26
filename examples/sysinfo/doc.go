// Copyright 2026 The Sokel Authors
// SPDX-License-Identifier: Apache-2.0

package main

// The user guide: a real markdown file, embedded at compile time (see docs/dev-playbook.md §5.0.0).

import _ "embed"

//go:embed docs/sysinfo.md
var usageDoc string
