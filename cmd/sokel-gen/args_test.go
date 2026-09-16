// Copyright 2026 The Sokel Authors
// SPDX-License-Identifier: Apache-2.0

package main

import (
	"flag"
	"os"
	"path/filepath"
	"testing"
)

// The usage line reads `sokel-gen init <dir> [-lang go|python|ts]`, and Go's flag package stops at
// the first non-flag argument — so following the help exactly parsed no flags and scaffolded Go
// whatever you asked for. Ignoring a flag is not an error, so nothing said a word.
func TestFlagsParseAfterPositionalArgs(t *testing.T) {
	newFS := func() (*flag.FlagSet, *string, *bool) {
		fs := flag.NewFlagSet("init", flag.ContinueOnError)
		lang := fs.String("lang", "go", "")
		manifest := fs.Bool("manifest", false, "")
		return fs, lang, manifest
	}

	for _, c := range []struct {
		name     string
		args     []string
		wantLang string
		wantMan  bool
		wantDir  string
	}{
		{"flags first", []string{"-lang", "python", "./x"}, "python", false, "./x"},
		{"dir first", []string{"./x", "-lang", "python"}, "python", false, "./x"},
		{"dir between", []string{"-manifest", "./x", "-lang", "go"}, "go", true, "./x"},
		{"equals form", []string{"./x", "-lang=ts"}, "ts", false, "./x"},
		{"bool last", []string{"./x", "-lang", "go", "-manifest"}, "go", true, "./x"},
		{"after --", []string{"-lang", "ts", "--", "-weird-dir"}, "ts", false, "-weird-dir"},
	} {
		t.Run(c.name, func(t *testing.T) {
			fs, lang, manifest := newFS()
			if err := fs.Parse(reorderArgs(fs, c.args)); err != nil {
				t.Fatalf("parsing failed: %v", err)
			}
			if *lang != c.wantLang || *manifest != c.wantMan {
				t.Errorf("lang=%q manifest=%v, want %q / %v", *lang, *manifest, c.wantLang, c.wantMan)
			}
			if fs.NArg() != 1 || fs.Arg(0) != c.wantDir {
				t.Errorf("positional args = %v, want [%s]", fs.Args(), c.wantDir)
			}
		})
	}
}

// **The call site, not just the helper.** Reverting runInit to fs.Parse(args) leaves the table above
// green — the bug was never in reorderArgs, it was in who called it.
func TestInitHonoursLangAfterDir(t *testing.T) {
	dir := t.TempDir() + "/p"
	if err := runInit([]string{dir, "-lang", "python"}); err != nil {
		t.Fatalf("init failed: %v", err)
	}
	if _, err := os.Stat(filepath.Join(dir, "main.py")); err != nil {
		t.Errorf("no main.py — `init <dir> -lang python` scaffolded something else: %v", err)
	}
	if _, err := os.Stat(filepath.Join(dir, "schema")); err == nil {
		t.Error("a schema/ directory was scaffolded — the -lang flag after the directory was ignored")
	}
}
