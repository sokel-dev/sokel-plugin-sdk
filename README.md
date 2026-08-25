# Sokel Plugin SDK

[![Go Reference](https://pkg.go.dev/badge/github.com/sokel-dev/sokel-plugin-sdk.svg)](https://pkg.go.dev/github.com/sokel-dev/sokel-plugin-sdk)
[![License](https://img.shields.io/badge/license-Apache--2.0-blue.svg)](LICENSE)

English · [简体中文](README.zh-CN.md)

Write a [Sokel](https://github.com/sokel-dev) plugin as a small Go program. Declare what your
operations take and return; the SDK handles registration, transport, credentials, file transfer,
heartbeats and reconnects.

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

## Why plugins dial out

A plugin **connects to the platform**, not the other way round. No inbound port, no public IP, no
firewall hole. A plugin running on a NAS in your basement is callable from the platform just like one
running in the cloud — which is also why something inherently local, like a coding agent on your own
laptop, can be a plugin at all.

## Install

The library:

```bash
go get github.com/sokel-dev/sokel-plugin-sdk
```

The `sokel-gen` CLI — scaffolds plugins and generates the typed code from your declarations:

```bash
go install github.com/sokel-dev/sokel-plugin-sdk/cmd/sokel-gen@latest
```

Requires Go 1.25 or newer. You can skip the install and use `go run
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
| `sokel-gen init <dir>` | Scaffold a new plugin that builds and runs as-is |
| `sokel-gen generate [dir...]` | Generate; a directory holding many plugins is walked automatically |
| `sokel-gen check [dir...]` | Verify the generated files are current, write nothing — for CI |
| `sokel-gen export <json\|ts\|python> [dir]` | Print the contract in another form |
| `sokel-gen migrate [dir]` | Turn an old struct+tag plugin into a `schema/` declaration |

`generate` and `check` take `-schema <name>` when the declaration package isn't called `schema`.

Plugins are found by **looking for a `schema/` directory**, not by reading `//go:generate` lines. That
distinction matters: `go generate ./...` silently skips a plugin whose directive someone forgot to
write, and a skipped plugin's contract drifts with nothing going red. Four first-party plugins were
in exactly that state before this was checked.

```bash
sokel-gen check ./plugins        # every plugin under ./plugins, one command
```

`check` runs every plugin before reporting, so CI shows you all the stale ones at once instead of one
per run.

## Example

[`examples/sysinfo`](examples/sysinfo) is a complete, runnable plugin: two operations, a file input,
an embedded user-facing doc.

```bash
cd examples/sysinfo
SOKEL_ENDPOINT=nats://localhost:4222 SOKEL_TOKEN=skp_xxx go run .
```

## One declaration, many targets

`sokel-gen` does not translate Go to Go. It parses your `schema/` package into a language-neutral
intermediate representation, then renders that IR through a backend of your choosing:

```
schema/ declaration ──▶ IR ──┬──▶ generate        zz_types.go / zz_register.go (typed Go)
                             ├──▶ export json    the contract itself, language-neutral
                             ├──▶ export ts      TypeScript contract table for a UI
                             └──▶ export python  pydantic models
```

```bash
sokel-gen export json    # feed this to any generator, in any language
```

The JSON deliberately omits Go type names — it carries the contract, not the Go implementation
detail, so a generator for another language has nothing to work around.

This matters because the wire protocol is JSON over NATS with base64 bytes: no gob, no protobuf,
nothing Go-specific. **This SDK is one implementation of that protocol, not the definition of it.**
An SDK for another language does not reverse-engineer Go — it reads the same exported contract and
generates its own types. Rust and Node.js backends are the intended next targets, and adding one is a
renderer over the existing IR rather than a second parser.

## Status

The Sokel platform itself is not open source yet. Until it is, this SDK is useful for reading the
plugin model and preparing a plugin — but a plugin needs a running Sokel instance to dial into.

## License

Apache-2.0. See [LICENSE](LICENSE).
