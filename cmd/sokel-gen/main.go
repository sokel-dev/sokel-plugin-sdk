// Copyright 2026 The Sokel Authors
// SPDX-License-Identifier: Apache-2.0

// Command sokel-gen is the contract toolchain for Sokel plugins.
//
// Contracts are declared with builders in a schema/ package; sokel-gen compiles and runs that
// declaration to read its value, then renders it for each target language. A mistake in the
// declaration is therefore a compile error, not a runtime surprise on some later call.
//
//	sokel-gen                       generate for the current directory (the //go:generate form)
//	sokel-gen init <dir>            scaffold a new plugin from scratch
//	sokel-gen generate [dir...]     generate; a directory holding many plugins is walked automatically
//	sokel-gen check [dir...]        verify generated files are current, write nothing (for CI)
//	sokel-gen export <format> [dir] export the contract: json (language-neutral) / yaml / ts / python
//	sokel-gen migrate [dir]         turn an old struct+tag plugin into a schema/ declaration
//	sokel-gen docs [topic]          print the format guide / JSON Schema / reference declaration
//	                                (embedded in the binary, readable offline)
//	sokel-gen example [lang]        print the reference plugin's declaration and both implementations
//
// Plugins are discovered **by looking for a schema/ directory or a manifest.yml**, not by reading
// //go:generate lines. That distinction matters: `go generate ./...` silently skips a plugin whose
// directive someone forgot to write — four first-party plugins were in exactly that state for
// months — while directory discovery cannot miss one.
package main

import (
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/sokel-dev/sokel-plugin-sdk/sokelgen"
)

func main() {
	if err := dispatch(os.Args[1:]); err != nil {
		fmt.Fprintln(os.Stderr, "sokel-gen:", err)
		os.Exit(1)
	}
}

func dispatch(args []string) error {
	// No arguments means "generate the current directory". Every existing plugin's //go:generate line
	// has that form, so it must keep working.
	if len(args) == 0 {
		return generate([]string{"."}, "schema", false, "")
	}
	switch cmd := args[0]; cmd {
	case "init":
		return runInit(args[1:])
	case "generate", "check":
		fs := flag.NewFlagSet(cmd, flag.ExitOnError)
		schema := fs.String("schema", "schema", "schema package directory, relative to the plugin root")
		lang := fs.String("lang", "", "target language for manifest plugins: ts / python (defaults to codegen.lang)")
		if err := fs.Parse(args[1:]); err != nil {
			return err
		}
		dirs := fs.Args()
		if len(dirs) == 0 {
			dirs = []string{"."}
		}
		return generate(dirs, *schema, cmd == "check", *lang)
	case "docs":
		return runDocs(args[1:])
	case "example":
		return runExample(args[1:])
	case "export":
		return runExport(args[1:])
	case "migrate":
		dir := "."
		if len(args) > 1 {
			dir = args[1]
		}
		return migrate(dir)
	case "help", "-h", "-help", "--help":
		usage(os.Stdout)
		return nil
	default:
		usage(os.Stderr)
		return fmt.Errorf("unknown subcommand %q", cmd)
	}
}

func usage(w *os.File) {
	fmt.Fprint(w, `sokel-gen — the contract toolchain for Sokel plugins

Usage:
  sokel-gen                       generate for the current directory (the //go:generate form)
  sokel-gen init <dir>            scaffold a new plugin (-lang go|python|ts)
  sokel-gen generate [dir...]     generate; walks a directory holding many plugins
  sokel-gen check [dir...]        verify generated files are current, write nothing (for CI)
  sokel-gen export <format> [dir] export the contract: json / yaml (language-neutral) / ts / python
  sokel-gen migrate [dir]         turn an old struct+tag plugin into a schema/ declaration
  sokel-gen docs [topic]          print the manifest.yml format guide (manifest / schema / example)
  sokel-gen example [lang]        print the reference plugin (yaml / python / node)

Options (generate / check):
  -schema <name>  schema package directory, default "schema"
  -lang <lang>    target language for manifest (manifest.yml) plugins: ts / python

A contract can be declared from two entry points, both producing the same contract:
  schema/ package   Go plugins (compile-time checks, reuses existing Go types)
  manifest.yml        language-neutral (Python / Node plugins), renders typed ts / python shells

Examples:
  sokel-gen init ./my-plugin
  sokel-gen check ./plugin-builtin             # check every plugin under that directory at once
  sokel-gen export json > contract.json
  sokel-gen export yaml ./plugins/gitlab       # Go declaration -> language-neutral manifest.yml
  sokel-gen generate -lang python ./my-plugin  # manifest.yml -> typed Python shell
`)
	agentHint(w)
}

// generate expands each given directory into a list of plugins and generates (or checks) each one.
//
// check mode **runs them all before reporting** rather than exiting on the first failure: seeing
// every stale plugin in one CI run beats fixing one and running again.
func generate(dirs []string, schemaSub string, check bool, lang string) error {
	var plugins []string
	for _, d := range dirs {
		found, err := discover(d, schemaSub)
		if err != nil {
			return err
		}
		if len(found) == 0 {
			return fmt.Errorf("no plugin found under %s (a plugin is a directory with a %s/ subdirectory, or a manifest.yml)", d, schemaSub)
		}
		plugins = append(plugins, found...)
	}
	sort.Strings(plugins)

	var stale []string
	for _, p := range plugins {
		if err := generateAny(p, schemaSub, check, len(plugins) > 1, lang); err != nil {
			if !check {
				return fmt.Errorf("%s: %w", p, err)
			}
			stale = append(stale, fmt.Sprintf("  %s: %v", p, err))
		}
	}
	if len(stale) > 0 {
		return fmt.Errorf("these plugins have stale generated files (declaration changed, nothing regenerated):\n%s\nfix: sokel-gen generate %s",
			strings.Join(stale, "\n"), strings.Join(dirs, " "))
	}
	if len(plugins) > 1 {
		verb := "generated"
		if check {
			verb = "up to date"
		}
		fmt.Printf("sokel-gen: %d plugins %s\n", len(plugins), verb)
	}
	return nil
}

// isPluginDir reports whether dir is a plugin: it has a schema/ subdirectory, or a manifest.yml.
//
// Discovery is by directory rather than by //go:generate directive: `go generate ./...` **silently
// skips** a plugin whose directive is missing (four first-party plugins were like that for months),
// and a drifted contract has no symptoms at all.
func isPluginDir(dir, schemaSub string) bool {
	if fi, err := os.Stat(filepath.Join(dir, schemaSub)); err == nil && fi.IsDir() {
		return true
	}
	mf, err := sokelgen.FindManifest(dir)
	return err == nil && mf != ""
}

// discover finds the plugins under root. Once a plugin is found it does not descend further —
// plugins do not nest, and continuing would only walk into the plugin's own subpackages.
func discover(root, schemaSub string) ([]string, error) {
	if isPluginDir(root, schemaSub) {
		return []string{root}, nil
	}
	var found []string
	err := filepath.WalkDir(root, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if !d.IsDir() {
			return nil
		}
		switch d.Name() {
		case ".git", "node_modules", "vendor", "testdata":
			return filepath.SkipDir
		}
		if isPluginDir(path, schemaSub) {
			found = append(found, path)
			return filepath.SkipDir
		}
		return nil
	})
	return found, err
}

func runExport(args []string) error {
	if len(args) == 0 {
		return fmt.Errorf("export needs a format: json / yaml / ts / python")
	}
	format := args[0]
	switch format {
	case "json", "yaml", "ts", "python":
	default:
		return fmt.Errorf("unknown format %q (json / yaml / ts / python)", format)
	}
	fs := flag.NewFlagSet("export", flag.ExitOnError)
	schema := fs.String("schema", "schema", "schema package directory, relative to the plugin root")
	if err := fs.Parse(args[1:]); err != nil {
		return err
	}
	dir := "."
	if fs.NArg() > 0 {
		dir = fs.Arg(0)
	}
	return export(dir, *schema, format)
}
