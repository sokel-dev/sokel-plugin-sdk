# Toolchain

## sokel-gen

One binary. It reads a contract declaration and writes the typed shell around it; it also carries
the format guide and the reference declaration, so it is the offline documentation as well.

**Download a binary** — no Go toolchain needed, which matters when your contract is a YAML file and
your plugin is Python or TypeScript:

```bash
# from the SDK's GitHub releases: sokel-gen_<version>_<os>_<arch>.tar.gz (.zip on Windows)
tar xzf sokel-gen_*_darwin_arm64.tar.gz && sudo mv sokel-gen /usr/local/bin/
sokel-gen version
```

Archives are published for darwin / linux / windows × amd64 / arm64, with a `checksums.txt`
alongside. With a Go toolchain already present, `go install
github.com/sokel-dev/sokel-plugin-sdk/cmd/sokel-gen@latest` does the same job.

**It is only used at authoring time** — the machine that runs the plugin never needs it, whatever
language the plugin is written in.

| Command | What it does |
|---|---|
| `sokel-gen init <dir> [-lang go\|python\|ts] [-manifest] [-module <path>]` | Scaffold a plugin that already runs end to end. `-manifest` gives a Go plugin declared in `manifest.yml` instead of a `schema/` package. Never overwrites an existing file |
| `sokel-gen generate [dir...]` | Declaration → typed shell. Walks a directory holding many plugins |
| `sokel-gen generate -lang go\|python\|ts <dir>` | For `manifest.yml` plugins: which shell to render. Go's output is several files, so its `codegen.out` names a **directory** |
| `sokel-gen check [dir...]` | Verify the generated files are current, write nothing. **Runs every plugin before reporting**, so one CI run lists all the stale ones |
| `sokel-gen export json\|yaml\|ts\|python [dir]` | Export the contract. `yaml` turns a Go `schema/` package into a language-neutral `manifest.yml` |
| `sokel-gen migrate [dir]` | Convert an old struct+tag plugin into a `schema/` declaration |
| `sokel-gen docs [manifest\|schema\|example]` | Print the format guide / the JSON Schema / the reference declaration |
| `sokel-gen example [yaml\|python\|node]` | Print the reference plugin |
| `sokel-gen` (no args) | Generate for the current directory — the `//go:generate` form |
| `sokel-gen version` | Which toolchain this is; a downloaded binary has no `go list` to fall back on |

`-schema <name>` changes the schema package directory (default `schema`).

A plugin is discovered as "a directory with a `schema/` subdirectory, or a `manifest.yml`", so
`sokel-gen check ./plugins` covers a whole repository at once.

Flags parse wherever you write them — `init ./x -lang python` and `init -lang python ./x` are the
same command.

## Language SDKs

| Language | Install | Contract declared in |
|---|---|---|
| Go | `go get github.com/sokel-dev/sokel-plugin-sdk` | `manifest.yml` **or** a `schema/` package |
| Python | `pip install sokel-plugin-sdk` | `manifest.yml` → `sokel_gen.py` |
| TypeScript | `npm install @sokel-dev/plugin-sdk` | `manifest.yml` → `sokel.gen.ts` |

All three speak the same wire protocol and report the same contract JSON. The Python and Node
packages are versioned together with the Go module; a reference plugin implemented in all three is
asserted against one golden contract, which is what keeps them from drifting apart.

## Environment variables

The SDK convention is that every plugin environment variable starts with `SOKEL_`. The Go helper
`sokel.Env("TOKEN")` adds the prefix for you.

| Variable | Meaning |
|---|---|
| `SOKEL_ENDPOINT` | Platform address the plugin dials |
| `SOKEL_TOKEN` | Access token of the access group it joins (`skp_…`) |
| `SOKEL_ACCESS` | Offline bundle: full access credentials exported by the platform, for dialing the broker directly when the platform is not reachable |
| `SOKEL_VERSION` | Version the plugin self-reports (usually baked into the image at build time) |
| `SOKEL_INSTANCE_ID` | Explicit replica identity; wins over the auto identity file |

**Deployment configuration belongs here, not in credentials.** The test: would this value still be
true for the same plugin deployed on another machine? If not, it is environment.

## Identity, when containerised

The SDK writes an automatic identity file (`.sokel-instance`) into the working directory. If the
working directory is not fixed, rebuilding the container changes the plugin's identity and the old
row becomes a ghost replica still claiming work. Either fix `WORKDIR` and mount a volume there, or
set `SOKEL_INSTANCE_ID` explicitly — an explicit identity always wins.
