// Copyright 2026 The Sokel Authors
// SPDX-License-Identifier: Apache-2.0

package main

import (
	"flag"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

// runInit scaffolds a plugin from nothing.
//
// What comes out **works end to end immediately**: go mod tidy -> sokel-gen -> go build. The hello
// operation in it is not a placeholder comment but a real operation that generates and compiles.
// Starting from something that runs saves a whole round of trial and error compared with assembling
// an empty directory from the documentation.
func runInit(args []string) error {
	fs := flag.NewFlagSet("init", flag.ExitOnError)
	module := fs.String("module", "", "go module path (defaults to the directory name; -lang go only)")
	lang := fs.String("lang", "go", "plugin language: go / python / ts")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if fs.NArg() == 0 {
		return fmt.Errorf("usage: sokel-gen init <dir> [-lang go|python|ts] [-module <path>]")
	}
	dir := fs.Arg(0)
	name := filepath.Base(filepath.Clean(dir))
	if name == "." || name == "/" || name == "" {
		return fmt.Errorf("give a concrete directory, e.g. sokel-gen init ./my-plugin")
	}
	mod := *module
	if mod == "" {
		mod = name
	}

	var files map[string]string
	switch *lang {
	case "go":
		files = scaffold(name)
	case "python":
		files = scaffoldPython(name)
	case "ts", "node":
		files = scaffoldTS(name)
	default:
		return fmt.Errorf("unknown language %q (go / python / ts)", *lang)
	}
	// Check everything before writing anything: better to write no bytes at all than half a plugin.
	for rel := range files {
		if _, err := os.Stat(filepath.Join(dir, rel)); err == nil {
			return fmt.Errorf("%s already exists — init never overwrites a file", filepath.Join(dir, rel))
		}
	}
	for rel, content := range files {
		path := filepath.Join(dir, rel)
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			return err
		}
		if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
			return fmt.Errorf("writing %s: %w", path, err)
		}
	}

	// Let go create go.mod: the version and the go directive come from the toolchain, and a
	// hand-written one drifts from it sooner or later.
	if *lang == "go" {
		if _, err := os.Stat(filepath.Join(dir, "go.mod")); os.IsNotExist(err) {
			cmd := exec.Command("go", "mod", "init", mod)
			cmd.Dir = dir
			if out, err := cmd.CombinedOutput(); err != nil {
				return fmt.Errorf("go mod init failed: %w\n%s", err, out)
			}
		}
	}

	fmt.Printf("sokel-gen: scaffolded a plugin in %s (%d files)\n\n", dir, len(files))
	fmt.Print(nextSteps(*lang, dir))
	return nil
}

// nextSteps are the lines to type next to get the scaffold running.
// Half a scaffold's value is the files, the other half is these lines.
func nextSteps(lang, dir string) string {
	switch lang {
	case "python":
		return fmt.Sprintf(`Next:
  cd %s
  pip install -r requirements.txt
  sokel-gen generate .     # sokel.yaml -> sokel_gen.py (typed models + registration)
  python main.py

Change the contract in sokel.yaml, then re-run sokel-gen generate .
`, dir)
	case "ts", "node":
		return fmt.Sprintf(`Next:
  cd %s
  npm install
  sokel-gen generate .     # sokel.yaml -> src/sokel.gen.ts (typed interfaces + registration)
  npm run build && npm start

Change the contract in sokel.yaml, then re-run sokel-gen generate .
`, dir)
	}
	return fmt.Sprintf(`Next:
  cd %s
  go mod tidy      # fetch the SDK
  sokel-gen        # schema/ -> zz_*.go
  go build ./...

Change the contract in schema/schema.go, then re-run sokel-gen.
`, dir)
}

// scaffold returns relative path -> contents.
//
// Both documents are there and neither is empty: README.md for whoever edits the code, docs/<name>.md
// for the user (the latter is embedded by doc.go, reported at registration and shown in the UI).
// A review sends back a plugin missing either one, so the scaffold does not start out missing them.
func scaffold(name string) map[string]string {
	const sdk = "github.com/sokel-dev/sokel-plugin-sdk"
	r := strings.NewReplacer("{{name}}", name, "{{sdk}}", sdk)

	return map[string]string{
		"schema/schema.go": r.Replace(`// Package schema declares the contract of {{name}}.
//
// Declaration only, no implementation: a contract is a public interface and should be reviewable on
// its own. One operation = one type + three methods (Meta / Inputs / Outputs). A misspelled method
// name (Input instead of Inputs) fails to compile, which is one reason this uses builders rather
// than struct tags.
package schema

import (
	"{{sdk}}/contract"
	"{{sdk}}/contract/field"
)

// Hello says hello. Replace it with your own first operation.
type Hello struct{}

func (Hello) Meta() contract.Meta {
	return contract.Meta{ID: "hello", Label: "Say hello"}
}

func (Hello) Inputs() []contract.FieldSpec {
	return []contract.FieldSpec{
		field.String("name").Label("Name").Desc("Who to greet"),
	}
}

func (Hello) Outputs() []contract.FieldSpec {
	return []contract.FieldSpec{
		field.String("greeting").Label("Greeting"),
	}
}
`),

		"main.go": r.Replace(`// {{name}} is a Sokel plugin.
//
// A plugin **dials out**: it connects back to the platform, so it needs no inbound port and no
// public IP.
//
// Run it:
//
//	SOKEL_ENDPOINT=nats://<broker>:4222 SOKEL_TOKEN=skp_xxx ./{{name}}
package main

//go:generate go run {{sdk}}/cmd/sokel-gen

import (
	"fmt"
	"log"

	"{{sdk}}/sokel"
)

func main() {
	token := sokel.Env("TOKEN")
	if token == "" {
		log.Fatal("set SOKEL_TOKEN (the plugin's access token, from the plugin admin page)")
	}
	p := sokel.New(sokel.Config{
		Endpoint: sokel.EnvOr("ENDPOINT", "nats://localhost:4222"),
		Token:    token,
		Name:     "{{name}}",
	})
	p.SetDoc(usageDoc, "") // the doc is reported at registration and shown in the UI

	// OnHello is generated by sokel-gen from schema/, as are HelloIn and HelloOut.
	// Change the declaration, re-run sokel-gen, and this signature changes with it — miss a spot and
	// it will not compile.
	OnHello(p, func(ctx sokel.Ctx, in *HelloIn) (*HelloOut, error) {
		return &HelloOut{Greeting: fmt.Sprintf("Hello, %s", in.Name)}, nil
	})

	if err := p.Run(); err != nil {
		log.Fatal(err)
	}
}
`),

		"doc.go": r.Replace(`package main

// The user-facing doc: a real markdown file, embedded at build time.

import _ "embed"

//go:embed docs/{{name}}.md
var usageDoc string
`),

		"docs/" + name + ".md": r.Replace(`# {{name}}

> For **users**: what this plugin does, what it needs, and what to watch out for.
> Whoever edits the code reads README.md instead.

## What it does

- **Say hello**: give it a name, get a greeting back.

## Configuration

| Environment variable | Required | Meaning |
|---|---|---|
| ` + "`SOKEL_ENDPOINT`" + ` | yes | Broker address, e.g. ` + "`nats://broker:4222`" + ` |
| ` + "`SOKEL_TOKEN`" + ` | yes | Access-group token (` + "`skp_…`" + `) |
`),

		"README.md": r.Replace(`# {{name}}

> For **whoever edits the code**. The user-facing document is ` + "`docs/{{name}}.md`" + `.

## Layout

| File | What it is |
|---|---|
| ` + "`schema/schema.go`" + ` | The contract declaration — which operations exist and what they take. **Edit this** |
| ` + "`zz_*.go`" + ` | Types and registration functions generated from the declaration. **Do not edit** |
| ` + "`main.go`" + ` | The handlers plus the wiring that dials back to the platform |
| ` + "`docs/{{name}}.md`" + ` | The user-facing document, embedded into the binary at build time |

## Development

` + "```bash" + `
sokel-gen            # regenerate after changing schema/
go build ./...
sokel-gen check      # for CI: verifies the generated files are current
` + "```" + `

The contract is **generated at build time**, not reflected at runtime: a mistake in the declaration
is a compile error. Changing the declaration and forgetting to regenerate turns ` + "`sokel-gen check`" + `
red — the most common way codegen fails.
`),
	}
}
