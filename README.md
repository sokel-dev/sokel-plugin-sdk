# Sokel Plugin SDK

[![Go Reference](https://pkg.go.dev/badge/github.com/sokel-dev/sokel-plugin-sdk.svg)](https://pkg.go.dev/github.com/sokel-dev/sokel-plugin-sdk)
[![PyPI](https://img.shields.io/pypi/v/sokel-plugin-sdk?label=pypi)](https://pypi.org/project/sokel-plugin-sdk/)
[![npm](https://img.shields.io/npm/v/@sokel-dev/plugin-sdk?label=npm)](https://www.npmjs.com/package/@sokel-dev/plugin-sdk)
[![License](https://img.shields.io/badge/license-Apache--2.0-blue.svg)](LICENSE)

English · [简体中文](README.zh-CN.md)

Write a [Sokel](https://github.com/sokel-dev) plugin in **Go, Python or TypeScript**, and let people
drag its operations onto a workflow canvas.

A plugin is one process that answers calls. You declare what each operation takes and returns; the
SDK handles registration, transport, credential delivery, file transfer, heartbeats and reconnects.
What you write is the part that is actually yours — the call to the service you are integrating.

```go
OnIssuesList(p, func(ctx sokel.Ctx, in *IssuesListIn) (*IssuesListOut, error) {
    issues, err := client.ListIssues(ctx, in.Project, in.State)
    if err != nil {
        return nil, err
    }
    return &IssuesListOut{Issues: issues, Count: len(issues)}, nil
})
```

That signature is **generated from your declaration**. There is no `map[string]any` anywhere in your
code, no hand-written parsing of inputs, and no second copy of the contract to keep in step. Python
and TypeScript work the same way — same generated shape, same guarantees, different syntax.

## What makes it work this way

**The contract is declared, not reflected.** A plugin's operations, their fields, its events and its
credential form are written down and turned into code before anything runs. A mistake in the
declaration fails the build; it does not surface on some later call to a service you no longer have
in front of you. The platform renders that same declaration on the canvas, so what a user configures
and what your handler receives cannot disagree.

**Plugins dial out.** A plugin connects to the platform, never the reverse — no inbound port, no
public IP, no firewall hole. A plugin on a NAS in your basement is callable exactly like one in the
cloud, which is also why something inherently local, such as a coding agent on your own laptop, can
be a plugin at all.

**The contract is language-neutral.** All three SDKs speak the same JSON-over-NATS protocol and
report the same contract JSON. A reference plugin ([`examples/kitchen-sink`](examples/kitchen-sink))
is declared once and implemented in every language, each asserted against one golden contract — so
the SDKs cannot quietly drift apart in how they read the protocol. This SDK is one implementation of
that protocol, not the definition of it.

## Letting an agent write the plugin

Writing a plugin suits a coding agent unusually well: every step has a command, an error and a
checkable result. Two things are prepared for that.

**A skill you can install.** [`skills/sokel-plugin-dev/`](skills/sokel-plugin-dev) is self-contained
and not tied to any one agent product — a `SKILL.md` entry point plus references covering the
toolchain, the manifest format, getting onto a platform, and the rules that fail *silently* when
broken. Copy the directory into wherever your agent keeps skills; for Claude Code that is
`~/.claude/skills/` or a project's `.claude/skills/`. Most of its references are generated from this
repository's own documentation and checked in CI, so the copy you install cannot go stale behind the
toolchain.

**Or four commands, if you would rather install nothing.** An agent can run commands and read
output, but often has neither a checkout of this repository nor access to GitHub. So the format
guide, the JSON Schema and a reference declaration covering every contract shape are embedded in the
`sokel-gen` binary:

```bash
sokel-gen docs                            # how to write manifest.yml
sokel-gen example                         # a real declaration using every shape
sokel-gen init <dir> -lang python|ts|go   # scaffold something that already runs
sokel-gen generate <dir>                  # the typed shell; every problem reported at once
```

Two lines are worth putting in the prompt, because agents get them wrong by default and nothing
complains:

- **Fixtures must be captured from the real upstream.** An invented fixture grows to match the
  agent's understanding of the API, which is exactly why it will always be green.
- **A field missing from the contract never reaches the handler, and no error is raised.** It looks
  like "I passed it and nothing happened" — the most common way a plugin is quietly broken.

## Declaring the contract

There are two entry points, and for Go **neither is the privileged one**:

| You write | Generate with | Why you would |
|---|---|---|
| `manifest.yml` | `sokel-gen generate -lang go\|python\|ts <dir>` | One language-neutral file — the same one you publish. Available in all three languages |
| a `schema/` package | `sokel-gen generate <dir>` | Go only: the contract is executable Go, so a misspelled method is a compile error and existing Go types are reused in place |

In Go both routes generate **the same API** — `OnXxx`, `RegisterCredential`, `DeclareEvents`,
`TriggerXxx` — so your implementation cannot tell which one produced it, and a plugin can move
between them untouched. `sokel-gen export yaml` goes the other way, turning a `schema/` package into
a manifest.

Default to the manifest unless you have a reason not to: it is one file, it is what gets published,
and a plugin ported between languages keeps its declaration instead of having it retyped. The format
is documented in [docs/manifest.md](docs/manifest.md) — YAML and JSON are the *same* format, parsed
through one path, and an unknown key is an error rather than a silently dropped field.

## Getting started

```bash
sokel-gen init ./my-plugin                 # -lang python|ts, or -lang go -manifest
cd my-plugin && sokel-gen generate . && go build ./...
```

What comes out **runs end to end already** — the `hello` operation in it is real, not a placeholder
comment, and it ships with both documents a plugin needs. Starting from something that runs saves a
round of guessing about how the pieces fit.

From there the loop is: change the declaration, regenerate, implement, run. `sokel-gen check` is what
CI runs — it fails when the declaration changed and nobody regenerated, which is how codegen usually
goes wrong and has no runtime symptom at all.

A plugin needs a platform to dial into. Creating the row there, getting an access token and running
the process are covered in the skill's
[`references/platform.md`](skills/sokel-plugin-dev/references/platform.md).

## Install

`sokel-gen` is only used while writing a plugin — the machine that runs it never needs the tool,
whatever language you chose. **A prebuilt binary needs no Go toolchain**, which matters when your
plugin is Python or TypeScript and your contract is a YAML file: take the archive for your platform
from this repository's releases (darwin / linux / windows × amd64 / arm64), drop `sokel-gen` on your
`PATH`, and `sokel-gen version` confirms it.

With Go 1.23+ already present, `go install github.com/sokel-dev/sokel-plugin-sdk/cmd/sokel-gen@latest`
does the same, and `go run github.com/sokel-dev/sokel-plugin-sdk/cmd/sokel-gen` — the `//go:generate`
form — pins the version to your `go.mod` rather than to whatever you last installed.

The libraries themselves:

| Language | Install | Getting started |
|---|---|---|
| Go | `go get github.com/sokel-dev/sokel-plugin-sdk` | this page |
| Python | `pip install sokel-plugin-sdk` | [sdk-python/README.md](sdk-python/README.md) |
| TypeScript | `npm install @sokel-dev/plugin-sdk` | [sdk-node/README.md](sdk-node/README.md) |

## Configuration

Everything comes from `SOKEL_`-prefixed environment variables. Exactly one of `SOKEL_TOKEN` /
`SOKEL_DEPLOY_KEY` / `SOKEL_ACCESS` must be set — they are three ways of proving the same thing.

| Variable | Required | Meaning |
|---|---|---|
| `SOKEL_ENDPOINT` | yes | The platform's `https://` URL. The SDK discovers broker credentials from it and can rediscover after a broker move. A literal `nats://broker:4222` still works as a legacy form but loses rediscovery |
| `SOKEL_TOKEN` | one of three | Access-group token (`skp_…`) identifying plugin + workspace |
| `SOKEL_DEPLOY_KEY` | one of three | Zero-touch enrollment for platform-shipped containers: enrolls on boot and mints its own access token |
| `SOKEL_ACCESS` | one of three | Offline connection bundle exported from the platform UI, for replicas that cannot reach its HTTP endpoint at all; skips discovery entirely |
| `SOKEL_NATS_CA` | no | Custom CA bundle for `tls://` brokers |
| `SOKEL_INSTANCE_ID` | no | Pin a replica's identity across restarts |
| `SOKEL_REGION` | no | Region label shown in the replica list |
| `SOKEL_VERSION` | no | Fallback for the version a replica self-reports |

Deployment configuration belongs here; **credentials do not**. The platform injects the resolved
credential fields with every call and the plugin never stores them. The test for which is which:
would this value still be true for the same plugin deployed on another machine? If not, it is
environment.

## The toolchain

| Command | What it does |
|---|---|
| `sokel-gen` | Generate for the current directory — the `//go:generate` form |
| `sokel-gen init <dir>` | Scaffold a plugin that builds and runs as-is (`-lang go｜python｜ts`, `-manifest`) |
| `sokel-gen generate [dir...]` | Generate; a directory holding many plugins is walked automatically |
| `sokel-gen check [dir...]` | Verify the generated files are current, write nothing — for CI |
| `sokel-gen export <json\|yaml\|ts\|python> [dir]` | Print the contract in another form |
| `sokel-gen migrate [dir]` | Turn an old struct+tag plugin into a `schema/` declaration |
| `sokel-gen docs [topic]` | The `manifest.yml` format guide / JSON Schema / reference declaration |
| `sokel-gen example [lang]` | The reference plugin: declaration and implementations |
| `sokel-gen version` | Which toolchain this is |

Plugins are discovered by **looking for a `schema/` directory or a `manifest.yml`**, not by reading
`//go:generate` lines — a directive someone forgot to write makes `go generate ./...` skip that
plugin silently, and its contract then drifts with nothing going red. Four first-party plugins were
in exactly that state before this was checked. `check` runs every plugin before reporting, so one CI
run lists all the stale ones rather than one per run.

## Packages

| Package | What it is |
|---|---|
| `sokel` | The runtime: register, dispatch, emit results, files, events, webhooks |
| `contract` | The contract types — field specs, metadata, credential and event shapes |
| `contract/field` | Builders for declaring fields (`field.String`, `field.Enum`, …) |
| `sokelgen` | The code generator behind `sokel-gen` |
| `cmd/sokel-gen` | The CLI |
| `pluginenv` | Reads the `SOKEL_` environment variables |

## Examples

| Example | What it shows |
|---|---|
| [`examples/sysinfo`](examples/sysinfo) | A complete Go plugin: two operations, a file input, an embedded user-facing doc |
| [`examples/kitchen-sink`](examples/kitchen-sink) | Every contract shape at once — declared once, implemented in Go, Python **and** TypeScript, all asserted against one golden contract |

## One declaration, many targets

```
schema/ package (Go builders) ──┐
                                ├──▶ IR ──┬──▶ typed Go      zz_types.go / zz_register.go / …
manifest.yml (language-neutral) ┘         ├──▶ typed Python  sokel_gen.py (pydantic models)
                                          ├──▶ typed TS      sokel.gen.ts (interfaces)
                                          ├──▶ export json   the contract itself
                                          └──▶ export yaml   a manifest, from a Go declaration
```

The exported JSON deliberately omits Go type names: it carries the contract, not an implementation
detail. The wire protocol is JSON over NATS with base64 bytes — no gob, no protobuf, nothing
Go-specific. A Rust SDK is the remaining target, and adding one is a renderer over the existing IR
plus a runtime, not a second parser.

## Releasing

One tag ships all three SDKs at the same version, plus the `sokel-gen` binaries — Go from the tag
itself, Python and TypeScript through
[`.github/workflows/release.yml`](.github/workflows/release.yml). The procedure and the one-time
registry setup are in [RELEASING.md](RELEASING.md).

Every gate in that pipeline exists because of a failure that only shows up **after** publishing:
version drift between the tag and the packages, stale generated files, a package whose build step
was skipped and therefore shipped empty. Neither npm nor PyPI lets you delete a version, so a bad
release can only be covered up by another one.

## Status

The Sokel platform itself is not open source yet. Until it is, this SDK is useful for reading the
plugin model and preparing a plugin — but a plugin needs a running Sokel instance to dial into.

## License

Apache-2.0. See [LICENSE](LICENSE).
