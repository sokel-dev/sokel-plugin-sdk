# Sokel Plugin SDK

[![Go Reference](https://pkg.go.dev/badge/github.com/sokel-dev/sokel-plugin-sdk.svg)](https://pkg.go.dev/github.com/sokel-dev/sokel-plugin-sdk)
[![PyPI](https://img.shields.io/pypi/v/sokel-plugin-sdk?label=pypi)](https://pypi.org/project/sokel-plugin-sdk/)
[![npm](https://img.shields.io/npm/v/@sokel-dev/plugin-sdk?label=npm)](https://www.npmjs.com/package/@sokel-dev/plugin-sdk)
[![License](https://img.shields.io/badge/license-Apache--2.0-blue.svg)](LICENSE)

English · [简体中文](README.zh-CN.md)

Write a [Sokel](https://github.com/sokel-dev) plugin in **Go, Python or TypeScript**. Declare what
your operations take and return; the SDK handles registration, transport, credentials, file
transfer, heartbeats and reconnects.

```go
OnIssuesList(p, func(ctx sokel.Ctx, in *IssuesListIn) (*IssuesListOut, error) {
    issues, err := client.ListIssues(ctx, in.Project, in.State)
    if err != nil {
        return nil, err
    }
    return &IssuesListOut{Issues: issues, Count: len(issues)}, nil
})
```

That handler signature is generated from your declaration. There is no `map[string]any` anywhere in
your code, and no second copy of the contract to keep in sync by hand.

The same is true in the other two languages — the declaration just lives in a language-neutral
`sokel.yaml` instead of a Go package:

```python
async def issues_list(ctx: Ctx, in_: IssuesListIn) -> IssuesListOut:
    issues = await client.list_issues(in_.project, in_.state)
    return IssuesListOut(issues=issues, count=len(issues))
```

```ts
onIssuesList(p, async (ctx, in_) => {
  const issues = await client.listIssues(in_.project, in_.state);
  return { issues, count: issues.length };
});
```

## Why plugins dial out

A plugin **connects to the platform**, not the other way round. No inbound port, no public IP, no
firewall hole. A plugin running on a NAS in your basement is callable from the platform just like one
running in the cloud — which is also why something inherently local, like a coding agent on your own
laptop, can be a plugin at all.

## Which SDK

| Language | Install | Declare the contract in | Getting started |
|---|---|---|---|
| Go | `go get github.com/sokel-dev/sokel-plugin-sdk` | a `schema/` package (Go builders) | below |
| Python | `pip install sokel-plugin-sdk` | `sokel.yaml` | [sdk-python/README.md](sdk-python/README.md) |
| TypeScript | `npm install @sokel-dev/plugin-sdk` | `sokel.yaml` | [sdk-node/README.md](sdk-node/README.md) |

All three speak the same JSON-over-NATS wire protocol and report the **same contract JSON**; a
reference plugin ([`examples/kitchen-sink`](examples/kitchen-sink)) is implemented twice and asserted
against one golden file, so the SDKs cannot drift apart in how they read the protocol.

## Install

The library:

```bash
go get github.com/sokel-dev/sokel-plugin-sdk
```

The `sokel-gen` CLI — scaffolds plugins and generates the typed code from your declarations:

```bash
go install github.com/sokel-dev/sokel-plugin-sdk/cmd/sokel-gen@latest
```

Requires Go 1.23 or newer. You can skip the install and use `go run
github.com/sokel-dev/sokel-plugin-sdk/cmd/sokel-gen` instead — that is the form used in `//go:generate`
lines, so the version is pinned by your `go.mod` rather than by whatever you last installed.

## How it works

Four steps, always in this order: **declare → generate → implement → connect**.

Start from a working skeleton rather than an empty directory:

```bash
sokel-gen init ./my-plugin
cd my-plugin && go mod tidy && sokel-gen && go build ./...
```

That scaffolds `schema/`, `main.go`, an embedded user-facing doc and both README files, with one real
operation already wired end to end. The rest of this section is what `init` gave you — change it into
your own plugin.

**1. Declare** the contract in a `schema/` package — inputs, outputs, events, credential fields:

```go
package schema

import (
	"github.com/sokel-dev/sokel-plugin-sdk/contract"
	"github.com/sokel-dev/sokel-plugin-sdk/contract/field"
)

type IssuesList struct{}

func (IssuesList) Meta() contract.Meta {
	return contract.Meta{ID: "issues_list", Label: "List issues"}
}

func (IssuesList) Inputs() []contract.FieldSpec {
	return []contract.FieldSpec{
		field.String("project").Label("Project"),
		field.Enum("state",
			field.Opt("opened", "Open"),
			field.Opt("closed", "Closed")).Default("opened"),
	}
}

func (IssuesList) Outputs() []contract.FieldSpec {
	return []contract.FieldSpec{
		field.Array("issues", []Issue{}).Label("Issues"),
		field.Int("count").Label("Count"),
	}
}
```

**2. Generate** the typed Go from that declaration:

```go
//go:generate go run github.com/sokel-dev/sokel-plugin-sdk/cmd/sokel-gen
```

```bash
go generate ./...
```

This writes `zz_types.go` (the `In`/`Out` structs) and `zz_register.go` (an `OnXxx` function per
operation). Don't hand-edit them.

The contract is produced **at compile time, not by runtime reflection**. A mistake in the declaration
fails the build instead of surfacing on some later call. `sokel-gen check` verifies the generated files
are current — wire it into CI, because forgetting to regenerate is the classic way codegen goes wrong.

**3. Implement** the handlers — the signatures are fully concrete, so the compiler checks your work.

**4. Connect** back to the platform:

```go
p := sokel.New(sokel.Config{
	Endpoint: sokel.Env("ENDPOINT"),
	Token:    sokel.Env("TOKEN"),
	Name:     "my-plugin",
})
OnIssuesList(p, handleIssuesList)
log.Fatal(p.Run())
```

## Configuration

The SDK reads everything from `SOKEL_`-prefixed environment variables:

| Variable | Required | Meaning |
|---|---|---|
| `SOKEL_ENDPOINT` | yes | `nats://broker:4222`, or an `https://` platform URL to discover the broker from |
| `SOKEL_TOKEN` | yes | Access-group token (`skp_…`) identifying plugin + workspace |
| `SOKEL_NATS_TOKEN` | no | Broker-level auth, if the broker requires it |
| `SOKEL_NATS_CA` | no | Custom CA bundle for `tls://` brokers |
| `SOKEL_INSTANCE_ID` | no | Pin a replica identity across restarts |
| `SOKEL_REGION` | no | Region label for the replica |

Credentials are never stored by the plugin. The platform injects the resolved fields with each call;
read them typed with `sokel.CredentialAs[T]`.

## Packages

| Package | What it is |
|---|---|
| `sokel` | The runtime: register, dispatch, emit results, files, events, webhooks |
| `contract` | The contract types — field specs, metadata, credential and event shapes |
| `contract/field` | Builders for declaring fields (`field.String`, `field.Enum`, …) |
| `sokelgen` | The code generator behind `sokel-gen` |
| `cmd/sokel-gen` | The CLI — see [below](#the-sokel-gen-cli) |
| `pluginenv` | Reads the `SOKEL_` environment variables |

## The `sokel-gen` CLI

| Command | What it does |
|---|---|
| `sokel-gen` | Generate for the current directory — the form used in `//go:generate` |
| `sokel-gen init <dir>` | Scaffold a new plugin that builds and runs as-is (`-lang go｜python｜ts`) |
| `sokel-gen generate [dir...]` | Generate; a directory holding many plugins is walked automatically |
| `sokel-gen check [dir...]` | Verify the generated files are current, write nothing — for CI |
| `sokel-gen export <json\|yaml\|ts\|python> [dir]` | Print the contract in another form |
| `sokel-gen migrate [dir]` | Turn an old struct+tag plugin into a `schema/` declaration |
| `sokel-gen docs [topic]` | Print the `sokel.yaml` format guide / JSON Schema / reference declaration |
| `sokel-gen example [lang]` | Print the reference plugin: declaration, Python impl, TypeScript impl |

`generate` and `check` take `-schema <name>` when the declaration package isn't called `schema`.

Plugins are found by **looking for a `schema/` directory or a `sokel.yaml`**, not by reading
`//go:generate` lines. That
distinction matters: `go generate ./...` silently skips a plugin whose directive someone forgot to
write, and a skipped plugin's contract drifts with nothing going red. Four first-party plugins were
in exactly that state before this was checked.

```bash
sokel-gen check ./plugins        # every plugin under ./plugins, one command
```

### Docs in the binary

The format guide, the JSON Schema and a reference declaration covering every contract shape are
**embedded in the `sokel-gen` binary** — no checkout, no network:

```bash
sokel-gen docs        # how to write sokel.yaml
sokel-gen example     # a real declaration using every shape; copy and edit
```

That is mostly for agents: pointing an LLM at four commands (`docs` → `example` →
`init -lang python|ts` → `generate`) is enough for it to write a working plugin, and `generate`
reports every problem in the declaration at once.

`check` runs every plugin before reporting, so CI shows you all the stale ones at once instead of one
per run.

## Examples

| Example | What it shows |
|---|---|
| [`examples/sysinfo`](examples/sysinfo) | A complete Go plugin: two operations, a file input, an embedded user-facing doc |
| [`examples/kitchen-sink`](examples/kitchen-sink) | Every contract shape at once — declared once, implemented in Python **and** TypeScript, both asserted against one golden contract |

```bash
cd examples/sysinfo
SOKEL_ENDPOINT=nats://localhost:4222 SOKEL_TOKEN=skp_xxx go run .
```

## One declaration, many targets

A contract can be declared from either entry point, and both produce the same intermediate
representation:

```
schema/ package (Go builders) ──┐
                                ├──▶ IR ──┬──▶ typed Go     zz_types.go / zz_register.go
sokel.yaml (language-neutral) ──┘         ├──▶ typed Python sokel_gen.py (pydantic models)
                                          ├──▶ typed TS     sokel.gen.ts (interfaces)
                                          ├──▶ export json  the contract itself
                                          └──▶ export yaml  a sokel.yaml, from a Go declaration
```

Go plugins use the `schema/` package: the contract is executable Go, a misspelled method is a
compile error, and existing Go types can be reused directly. Python and TypeScript plugins use
`sokel.yaml` — declaring a few fields should not start with "learn a Go builder API".

```bash
sokel-gen init -lang python ./my-plugin   # or -lang ts
sokel-gen generate ./my-plugin            # sokel.yaml → typed models + registration
sokel-gen export yaml ./plugins/gitlab    # the reverse: Go declaration → sokel.yaml
```

The format is documented in [docs/manifest.md](docs/manifest.md). YAML and JSON are the *same*
format (parsed through one path), and unknown keys are an error rather than a silently dropped
field.

The exported JSON deliberately omits Go type names — it carries the contract, not the Go
implementation detail. This matters because the wire protocol is JSON over NATS with base64 bytes:
no gob, no protobuf, nothing Go-specific. **This SDK is one implementation of that protocol, not the
definition of it.** A Rust SDK is the remaining target, and adding one is a renderer over the
existing IR plus a runtime, not a second parser.

## Releasing

One tag ships all three SDKs at the same version — Go from the tag itself, Python and TypeScript
through [`.github/workflows/release.yml`](.github/workflows/release.yml). The procedure and the
one-time registry setup are in [RELEASING.md](RELEASING.md).

```bash
# bump sdk-node/package.json + sdk-python/pyproject.toml, then
git tag v0.3.0 && git push origin main --tags
```

Every gate in that pipeline exists because of a failure that only shows up **after** publishing:
version drift between the tag and the packages, stale generated files, a package whose build step
was skipped and therefore ships empty.

## Status

The Sokel platform itself is not open source yet. Until it is, this SDK is useful for reading the
plugin model and preparing a plugin — but a plugin needs a running Sokel instance to dial into.

## License

Apache-2.0. See [LICENSE](LICENSE).
