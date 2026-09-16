// Copyright 2026 The Sokel Authors
// SPDX-License-Identifier: Apache-2.0

package main

import (
	"flag"
	"strings"
)

// reorderArgs moves flags ahead of positional arguments so they parse wherever they were written.
//
// Go's flag package stops at the first non-flag argument, and this tool's own help puts the
// directory first: `sokel-gen init ./my-plugin -lang python` therefore parsed **no flags at all**
// and scaffolded a Go plugin — silently, since ignoring a flag is not an error. Following the usage
// line as printed produced the wrong language.
//
// A flag's value has to travel with it, so a non-boolean flag written as two tokens takes the next
// one along. `--` ends flag parsing, as usual.
func reorderArgs(fs *flag.FlagSet, args []string) []string {
	var flags, pos []string
	for i := 0; i < len(args); i++ {
		a := args[i]
		if a == "--" {
			pos = append(pos, args[i+1:]...)
			break
		}
		if len(a) > 1 && a[0] == '-' {
			flags = append(flags, a)
			name := strings.TrimLeft(a, "-")
			if strings.Contains(name, "=") {
				continue // -lang=python carries its own value
			}
			f := fs.Lookup(name)
			if f == nil {
				continue // unknown: let flag.Parse report it
			}
			if bf, ok := f.Value.(interface{ IsBoolFlag() bool }); ok && bf.IsBoolFlag() {
				continue // a bool flag never consumes the next token
			}
			if i+1 < len(args) {
				i++
				flags = append(flags, args[i])
			}
			continue
		}
		pos = append(pos, a)
	}
	if len(pos) == 0 {
		return flags
	}
	// Terminate with `--`: a positional that begins with a dash (a directory literally named
	// "-weird") would otherwise be re-read as an unknown flag once it has been moved to the back.
	return append(append(flags, "--"), pos...)
}
