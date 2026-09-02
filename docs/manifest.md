# `manifest.yml` — the language-neutral contract declaration

[简体中文](manifest.zh-CN.md)

A plugin's contract can be declared from **two entry points**, both producing the same IR:

```
schema/ package (Go builders) ──┐
                                ├─▶ IR ─▶ render Go / TypeScript / Python
manifest.yml (this document) ─────┘
```

Go plugins use the `schema/` package: the contract is executable Go, a misspelled method is a
compile error, and existing Go types can be reused directly. Python and TypeScript plugins use
`manifest.yml` — declaring a few fields should not start with "install a Go toolchain and learn a
builder API".

Top-level keys follow the wire protocol's snake_case (`events_common`, `doc_url`); keys inside a
field follow the protocol's Field shape, which is camelCase (`valueType`, `oneOf`, `itemType`,
`timeoutSec`). **Both spellings are accepted** (`eventsCommon` == `events_common`), so copying a
line straight out of the protocol doc never lands you on an "unknown field".

YAML and JSON are the **same format** (`manifest.json` works too): YAML is converted to JSON and then
decoded through one path, so a key can never be "supported in YAML but not in JSON". Decoding
rejects unknown keys — a misspelled `lable:` fails loudly instead of being silently dropped, which
is the classic way a declarative format fails.

## Reading this offline

The guide, the schema and the reference declaration are embedded in the `sokel-gen` binary. No
checkout, no network:

```bash
sokel-gen docs            # this document
sokel-gen docs schema     # the JSON Schema
sokel-gen example         # a declaration using every shape; copy and edit
sokel-gen example python  # the Python implementation of that declaration
sokel-gen example node    # the TypeScript implementation
```

Pointing an LLM at four commands is enough for it to write a working plugin: `docs` to learn the
format, `example` to copy from, `init -lang python|ts` to scaffold, `generate` to build the typed
shell (which reports every problem in the declaration at once).

## Editor completion and validation

[`sokel.schema.json`](sokel.schema.json) in this directory is the JSON Schema for the format. Put it
on the first line of your `manifest.yml` and VS Code (YAML extension) or JetBrains will validate **as
you type** — key completion, enum candidates, typos underlined immediately, instead of at the next
`sokel-gen` run:

```yaml
# yaml-language-server: $schema=https://raw.githubusercontent.com/sokel-dev/sokel-plugin-sdk/main/docs/sokel.schema.json
```

`sokel-gen init` puts that line in the scaffold for you. Offline, or to pin a version, replace the
URL with a local relative path.

The schema is a second definition of the same format, so a test keeps the two from drifting
(`TestJSONSchemaMatchesParser`: every key the parser accepts must appear in the schema, and every
key the schema lists must be one the parser accepts).

## Where the file lives

`sokel-gen` finds plugins by directory: a directory is a plugin if it contains a `schema/`
subdirectory **or** a `manifest.yml`. Candidate names, in order: `manifest.yml`, `manifest.yml`,
`manifest.json`. Having more than one is an error — that usually means a rename left a stale copy.

## Scaffold

```bash
sokel-gen init -lang python ./my-plugin   # or -lang ts
cd my-plugin && sokel-gen generate .
```

## Top-level structure

```yaml
plugin:                 # identity and the user-facing doc
  name: gitlab
  label: GitLab
  desc: Repos, MRs, issues, CI
  version: 1.0.0
  doc: docs/gitlab.md   # PATH to the user-facing markdown (relative to this file); inlined at generation time
  doc_url: https://…    # use this instead if you already have a doc site; don't copy one in

capabilities:           # optional capability self-report: how far a given operation goes
  recency: false

credential:             # credential contract + how the credential is obtained
  auth: { kind: input }
  fields: [ <Field>… ]

events_common: [chat_id]  # fields every event carries; flattened to the top level when triggering
events:                   # event contracts
  - { id: message, label: Message received, fields: [ <Field>… ] }

operations:               # operation contracts
  - id: issues_list
    label: List issues
    desc: …
    stream: false         # streaming: reply frame by frame
    timeoutSec: 120       # suggested timeout; declare it for heavy work — the platform default is 60s
    inputs: [ <Field>… ]
    outputs: [ <Field>… ]

codegen:                  # generation targets; one, or a list
  - { lang: python, out: sokel_gen.py }
  - { lang: ts, out: src/sokel.gen.ts }
```

`plugin.label` / `desc` / `version` do not go into the registration handshake (the platform's display
name comes from its plugin catalogue), but they do go into the generated file: **anything you declare
must be visible somewhere**, otherwise changing it would not even turn `sokel-gen check` red.
`version` has a further effect — the generated `new_plugin()` / `newPlugin()` reports it as the
replica's version.

## Field

Field shapes map one to one onto the wire protocol's `Field`:

| Key | Meaning |
|---|---|
| `name` | The contract name, and the key in the runtime value. **Renaming replaces the field** (references on the canvas break) |
| `label` / `desc` | Display name and description; `desc` is the **required reason** when `opaque` is set |
| `type` | `string` / `text` / `number` / `boolean` / `file` / `json` / `array` / `enum` / `secret` |
| `required` | Required. Decides whether the generated type gets a default |
| `default` | Default value (a default implies not required) |
| `options` | `enum` candidates: bare strings, or `{value, label}` (give a label only when the value is a code no human reads) |
| `fields` | Sub-fields of `json` / element fields of `array` (recursive) |
| `valueType` | Dynamic keys: keys known only at runtime, values all one type. **Mutually exclusive** with `fields` |
| `itemType` | Scalar element type of an array (`string` / `number` / `boolean` / `file`) |
| `goType` | **Names** this structure; a later field can reference the name and omit `fields` (see below) |
| `opaque` | Declares "no structure". Only `json` / `array` may set it, and **`desc` is mandatory** |
| `oneOf` | Structural union: accepts one of the listed shapes |
| `types` | Scalar union (e.g. `number｜string`): variable binding accepts either |

### Shorthands

| Written as | Equivalent to |
|---|---|
| `type: int` | `type: number` + `goType: int` (generates `int`, not a float) |
| `type: files` | `type: array` + `itemType: file` |
| `type: strings` | `type: array` + `itemType: string` |
| `type: ints` | `type: array` + `itemType: number` + `goType: int` |

### Declare a structure once, reference it by name

```yaml
inputs:
  - { name: profile, type: json, goType: Profile, fields: [ { name: nick, type: string } ] }
outputs:
  - { name: profile, type: json, goType: Profile }     # no need to repeat the fields
```

Repeating them is the actual risk: two copies drift, and then the platform sees two structures with
the same name, the same shape, and different contents — with no way to tell which one is right.
Referencing a name nobody defined is an **error**, not a silently empty type.

### `opaque` requires a reason

```yaml
- name: extra
  type: json
  opaque: true
  desc: Passed through from the caller; the shape is decided upstream
```

"I couldn't be bothered" and "this genuinely has no structure" look identical in a file. The reason
is the only thing that separates them, so `opaque` without `desc` is rejected.

### `oneOf`: the runtime value *is* the branch

```yaml
- name: doc
  type: json
  oneOf:
    - { name: DocObject, type: json, fields: [ { name: title, type: string, required: true } ] }
    - { name: Block, type: array, fields: [ { name: kind, type: string, required: true } ] }
```

No discriminator wrapper — that would add a level to every downstream reference path. The generated
type is `Union[DocObject, List[Block]]` (Python) or `DocObject | Block[]` (TypeScript); the handler
tells the branches apart by shape.

## Events and common fields

```yaml
events_common: [chat_id]
events:
  - { id: message,   fields: [ { name: chat_id, type: string, required: true }, … ] }
  - { id: heartbeat, fields: [ { name: chat_id, type: string, required: true }, … ] }
```

A common field must exist in **every** event with the same type, or generation fails. The
intersection is deliberately not inferred: adding an event that happens to omit a field would
silently shrink the common set and break existing workflows — and nobody would think to look here.
Common fields also may not collide with the reserved keys (`_event`, `event`, `input`,
`credential_id`) or with an event id.

## Credentials and authentication

```yaml
credential:
  auth: { kind: qr }                                   # or input / oauth
  fields:
    - { name: api_key, label: API Key, type: secret, required: true }
```

`kind` determines the **steps**; you neither need nor may write them out:

| kind | Steps | Implemented by |
|---|---|---|
| `qr` | start + poll | the plugin (renders a code, polls for confirmation) |
| `input` | start + poll + submit | the plugin (one more step: the user types something back) |
| `oauth` | none | **the platform** (the client secret lives there; a plugin cannot build the consent URL) |

`kind: oauth` requires `provider` and may set `scopes`. Declaring auth automatically adds three
internal operations to the contract — `auth.start` / `auth.poll` / `auth.submit` — because the
platform's UI builds its requests from the contract and would not know what to send otherwise.
Business operation ids are restricted to `^[a-z][a-z0-9_]*$`; the dotted namespace belongs to the
platform, so the two cannot collide.

### `health_check`: credential health by convention

Credentials go stale (keys revoked, cookies expired). The platform does not guess — it asks the
plugin, through an operation with a **conventional id**:

```yaml
- id: health_check    # what the credential page's "Test" button calls
  label: Credential check
  inputs: []
  outputs:
    - { name: ok, label: Usable, type: boolean, required: true }
    - { name: message, label: Detail, type: string }
```

**Return `ok=false` for an unusable credential; do not raise.** Raising leaves the platform able to
say only "the call failed", while "the key expired, re-authorize" and "the network is unreachable,
check your proxy" are two entirely different things for the user to do. Declaring the operation is
optional — without it, that plugin's credentials can only be discovered as broken by a real call.

## Generating and checking

```bash
sokel-gen generate ./my-plugin      # generate per `codegen` (multiple targets allowed)
sokel-gen generate -lang ts .       # generate just one of them
sokel-gen check ./plugins           # CI: red if the declaration changed and nothing was regenerated
sokel-gen export json ./my-plugin   # the contract itself, language-neutral
sokel-gen export yaml ./go-plugin   # the reverse: a Go schema/ declaration → manifest.yml
```

`export yaml` exists for implementing an existing plugin in another language: start from the
first-party declaration without reading Go, and without letting the Go version become the de facto
standard by accident.

## One example with every shape

[`examples/kitchen-sink/manifest.yml`](../examples/kitchen-sink/manifest.yml) uses every shape above once
and is implemented twice, in `python/` and `node/`. Both implementations must report a contract equal
to the same [`contract.golden.json`](../examples/kitchen-sink/contract.golden.json), and three tests
(Go, Python, Node) assert exactly that.
