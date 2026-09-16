// Copyright 2026 The Sokel Authors
// SPDX-License-Identifier: Apache-2.0

package main

// Scaffolds for Python and Node plugins.
//
// The only structural difference from the Go scaffold: the contract is declared in a
// language-neutral manifest.yml rather than a schema/ package. Everything else follows the same rules
// — both documents are present (README for whoever edits the code, docs/ for the user), and the
// hello operation **works end to end**, it is not a placeholder comment.

import "strings"

func scaffoldPython(name string) map[string]string {
	r := strings.NewReplacer("{{name}}", name)
	return map[string]string{
		"manifest.yml":         r.Replace(manifestTemplate) + "codegen:\n  - { lang: python, out: sokel_gen.py }\n",
		"main.py":              r.Replace(pyMain),
		"requirements.txt":     "sokel-plugin-sdk>=0.3\n",
		"docs/" + name + ".md": r.Replace(userDoc),
		"README.md":            r.Replace(devDoc) + r.Replace(pyDevDoc),
		".gitignore":           "__pycache__/\n.venv/\n.sokel-instance-id*\n",
	}
}

// scaffoldGoManifest is a Go plugin whose contract lives in manifest.yml.
//
// The same choice Python and TypeScript have, given to Go: a manifest is the artifact distribution
// wants anyway, and requiring a schema/ package meant a Go author could not start from the file
// everyone else starts from. What comes out of generation is the same API either way — OnXxx,
// RegisterCredential, DeclareEvents — so the implementation cannot tell which route produced it.
func scaffoldGoManifest(name string) map[string]string {
	r := strings.NewReplacer("{{name}}", name, "{{sdk}}", "github.com/sokel-dev/sokel-plugin-sdk")
	return map[string]string{
		"manifest.yml":         r.Replace(manifestTemplate) + "codegen:\n  - { lang: go }   # Go's out is a directory; empty means next to the manifest\n",
		"main.go":              r.Replace(goManifestMain),
		"docs/" + name + ".md": r.Replace(userDoc),
		"README.md":            r.Replace(devDoc) + r.Replace(goManifestDevDoc),
		".gitignore":           ".sokel-instance-id*\n",
	}
}

const goManifestMain = `// {{name}} is a Sokel plugin. Its contract is declared in manifest.yml.
//
// A plugin **dials out**: it connects back to the platform, so it needs no inbound port and no
// public IP.
//
// Run it:
//
//	SOKEL_ENDPOINT=http://<platform> SOKEL_TOKEN=skp_xxx go run .
package main

//go:generate go run {{sdk}}/cmd/sokel-gen generate -lang go .

import (
	"fmt"
	"log"

	"{{sdk}}/plugin"
	"{{sdk}}/sokel"
)

func main() {
	token := sokel.Env("TOKEN")
	if token == "" {
		log.Fatal("set SOKEL_TOKEN (the access group's token, from the plugin's Access tab)")
	}
	p := sokel.New(sokel.Config{
		Endpoint: sokel.EnvOr("ENDPOINT", "http://localhost:8088"),
		Token:    token,
		Name:     "{{name}}",
	})
	RegisterCredential(p) // generated from the credential section of manifest.yml
	RegisterDoc(p)        // the guide travels with the plugin and is shown in the UI

	// OnHello, HelloIn and HelloOut are generated from manifest.yml. Change the declaration there,
	// re-run sokel-gen, and this signature changes with it — miss a spot and it will not compile.
	OnHello(p, func(ctx plugin.Ctx, in *HelloIn) (*HelloOut, error) {
		return &HelloOut{Greeting: fmt.Sprintf("Hello, %s", in.Name)}, nil
	})

	if err := p.Run(); err != nil {
		log.Fatal(err)
	}
}
`

const goManifestDevDoc = `
## Working on it

    go mod tidy
    sokel-gen generate -lang go .   # manifest.yml -> zz_*.go
    go build ./...

The contract is **manifest.yml**; the zz_*.go files are generated from it and are not edited by hand.
` + "`sokel-gen check .`" + ` is what CI runs: it fails when the declaration changed and nothing was regenerated.
`

func scaffoldTS(name string) map[string]string {
	r := strings.NewReplacer("{{name}}", name)
	return map[string]string{
		"manifest.yml":         r.Replace(manifestTemplate) + "codegen:\n  - { lang: ts, out: src/sokel.gen.ts }\n",
		"src/main.ts":          r.Replace(tsMain),
		"package.json":         r.Replace(tsPackage),
		"tsconfig.json":        tsConfig,
		"docs/" + name + ".md": r.Replace(userDoc),
		"README.md":            r.Replace(devDoc) + r.Replace(tsDevDoc),
		".gitignore":           "node_modules/\ndist/\n.sokel-instance-id*\n",
	}
}

const manifestTemplate = `# yaml-language-server: $schema=https://raw.githubusercontent.com/sokel-dev/sokel-plugin-sdk/main/docs/sokel.schema.json
# The contract of {{name}} — language-neutral. Edit this file.
#
# After editing, re-run: sokel-gen generate .
# (the output is typed models and registration functions; do not edit it by hand)
#
# What a field can declare: run "sokel-gen docs", or read docs/manifest.md in the SDK.
# An example using every shape: "sokel-gen example".

plugin:
  name: {{name}}
  label: {{name}}
  version: 0.1.0
  doc: docs/{{name}}.md

# The credential contract: the platform injects the resolved fields with every call, and the plugin
# never stores them. Delete the whole section if you do not need one.
credential:
  fields:
    - { name: api_key, label: API key, type: secret, required: true }

operations:
  - id: hello
    label: Say hello
    desc: Give it a name, get a greeting back — replace it with your own first operation.
    inputs:
      - { name: name, label: Name, type: string, required: true }
    outputs:
      - { name: greeting, label: Greeting, type: string, required: true }

`

const pyMain = `"""{{name}} — a Sokel plugin (Python).

A plugin **dials out**: it connects back to the platform, so it needs no inbound port and no public
IP.

Run it:

    SOKEL_ENDPOINT=nats://<broker>:4222 SOKEL_TOKEN=skp_xxx python main.py
"""

import asyncio
import logging

from sokel import Ctx

# Generated from manifest.yml: change the declaration, then re-run sokel-gen generate .
from sokel_gen import HelloIn, HelloOut, new_plugin, on_hello

logging.basicConfig(level=logging.INFO, format="%(asctime)s %(message)s")

p = new_plugin()


async def hello(ctx: Ctx, in_: HelloIn) -> HelloOut:
    return HelloOut(greeting=f"Hello, {in_.name}")


on_hello(p, hello)


if __name__ == "__main__":
    asyncio.run(p.run())
`

const tsMain = `/**
 * {{name}} — a Sokel plugin (TypeScript).
 *
 * A plugin **dials out**: it connects back to the platform, so it needs no inbound port and no
 * public IP.
 *
 * Run it:
 *
 *     SOKEL_ENDPOINT=nats://<broker>:4222 SOKEL_TOKEN=skp_xxx node dist/main.js
 */

import type { Ctx } from "@sokel-dev/plugin-sdk";

// Generated from manifest.yml: change the declaration, then re-run sokel-gen generate .
import { newPlugin, onHello } from "./sokel.gen.js";
import type { HelloIn, HelloOut } from "./sokel.gen.js";

const p = newPlugin();

onHello(p, (_ctx: Ctx, in_: HelloIn): HelloOut => ({ greeting: ` + "`Hello, ${in_.name}`" + ` }));

await p.run();
`

const tsPackage = `{
  "name": "{{name}}",
  "version": "0.1.0",
  "private": true,
  "type": "module",
  "scripts": {
    "build": "tsc -p tsconfig.json",
    "start": "node dist/main.js"
  },
  "dependencies": {
    "@sokel-dev/plugin-sdk": "^0.3.0"
  },
  "devDependencies": {
    "@types/node": "^22.10.0",
    "typescript": "^5.7.0"
  }
}
`

const tsConfig = `{
  "compilerOptions": {
    "target": "ES2022",
    "module": "NodeNext",
    "moduleResolution": "NodeNext",
    "lib": ["ES2023"],
    "strict": true,
    "outDir": "dist",
    "rootDir": "src",
    "types": ["node"],
    "skipLibCheck": true
  },
  "include": ["src/**/*.ts"]
}
`

const userDoc = `# {{name}}

> For **users**: what this plugin does, what it needs, and what to watch out for.
> Whoever edits the code reads README.md instead.

## What it does

- **Say hello**: give it a name, get a greeting back.

## Credentials

| Field | Required | Meaning |
|---|---|---|
| API key | yes | The upstream service's key; stored masked |
`

const devDoc = `# {{name}}

> For **whoever edits the code**. The user-facing document is ` + "`docs/{{name}}.md`" + `.

## Layout

| File | What it is |
|---|---|
| ` + "`manifest.yml`" + ` | The contract declaration — which operations exist and what they take. **Edit this** |
| generated file | Types and registration functions built from the declaration. **Do not edit** |
| entry point | The handlers plus the wiring that dials back to the platform |
| ` + "`docs/{{name}}.md`" + ` | The user-facing document, reported at registration and shown in the UI |

The contract is **generated**, not hand-written: change ` + "`manifest.yml`" + ` without regenerating and
` + "`sokel-gen check .`" + ` turns red — the most common way codegen fails, and CI stops it there.

`

const pyDevDoc = "## Development\n\n```bash\npip install -r requirements.txt\nsokel-gen generate .   # regenerate after changing manifest.yml\npython main.py\nsokel-gen check .      # for CI\n```\n"

const tsDevDoc = "## Development\n\n```bash\nnpm install\nsokel-gen generate .   # regenerate after changing manifest.yml\nnpm run build && npm start\nsokel-gen check .      # for CI\n```\n"
