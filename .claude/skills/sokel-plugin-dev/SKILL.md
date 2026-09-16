---
name: sokel-plugin-dev
description: >-
  Write, change, or review a Sokel plugin — a process that dials back into a
  Sokel platform and exposes typed operations, events, webhooks and credentials.
  Use it when the task is "write a plugin for <service>", "add an operation /
  event / webhook", "why doesn't the platform see my field", "port this plugin
  to Python/TypeScript", or when reviewing plugin code. Covers the toolchain
  (sokel-gen), the declare-then-implement order, how a plugin gets installed
  (manifest, not hand-filled forms), and the rules that fail silently when broken.
---

# Writing a Sokel plugin

A plugin is one process. It **dials out** to the platform (no inbound port, no public
IP), reports a **contract** — which operations it has, what each takes and returns,
which events it can emit, what its credential form looks like — and then answers calls.

The contract is declared, never reflected at runtime, and **declared before implemented**:
it is a public interface, reviewable on its own, and it is what the platform renders on
the canvas.

## The toolchain is the documentation

`sokel-gen` carries the format guide and a full reference declaration. Read them from the
tool rather than guessing at YAML keys:

```bash
sokel-gen docs                        # the complete manifest.yml format guide
sokel-gen example                     # a real declaration using every shape, to copy
sokel-gen init <dir> [-lang go|python|ts]
sokel-gen generate <dir>              # declaration -> typed shell; reports every problem at once
sokel-gen check <dir>                 # CI: red if the generated files are stale
sokel-gen export yaml <dir>           # Go schema/ package -> language-neutral manifest.yml
```

`sokel-gen docs schema` prints the JSON Schema for `manifest.yml` instead of the prose guide. `examples/kitchen-sink` in this
repo is the golden declaration — the one the three language generators are pinned against.

## Where the contract lives

| Language | Declared in | Why |
|---|---|---|
| Go | `schema/` package (`field.*` builders) | Executable: a misspelled method name fails to compile, and existing Go types can be reused |
| Python / TypeScript | `manifest.yml` | Declaring a few fields should not require a Go toolchain first |

Both entry points produce **the same contract JSON**. The platform does not know, and does
not need to know, which language a plugin is written in.

## The order of work

1. **Scaffold** — `sokel-gen init ./my-plugin`. What comes out runs end to end already
   (`go mod tidy` → `sokel-gen` → `go build`); the `hello` operation in it is real, not a
   placeholder comment. Start from something that runs.
2. **Declare** the contract (`schema/` or `manifest.yml`). Declaration only, no implementation.
3. **Generate** — `sokel-gen generate .` produces the typed shell (`zz_*.go` / `sokel_gen.py` /
   `sokel.gen.ts`). **Never hand-edit generated files**; the next run overwrites them and CI
   catches it.
4. **Implement** the handlers. Signatures are fully concrete — no `any`, no generics.
5. **Install into a platform** — see below. This is where the access token comes from.
6. **Run it** (`SOKEL_ENDPOINT` + `SOKEL_TOKEN`), then exercise one operation from the
   platform's debug console. That console is not a simulation: a write operation really writes.
7. **Before committing** — `sokel-gen check .`, build, vet, test, and both docs (below).

## Installing: the manifest builds the platform row, not a form

A plugin process authenticates with a **group-level access token**, so the platform needs a
plugin row and an access group before the process can connect. **That row is created from the
manifest** — id, name, credential form, operations, deployment — via *Plugins → Import manifest*.
The platform creates the row, a default channel, a default NATS access group, and hands back
the token. Nothing is typed into a form, so nothing can disagree with the code.

For a Go plugin, export the manifest instead of writing a second copy by hand:

```bash
sokel-gen export yaml ./my-plugin > my-plugin/manifest.yml
```

Two things follow from how install works:

- **The first import can be minimal.** Once the process connects, its self-reported contract
  overrides what was imported — so changing the contract means restarting the process, not
  reinstalling. Re-installing the same name is rejected (409) on purpose.
- **A manifest-installed plugin is third-party by origin**: it lives in the current workspace
  with no catalog entry behind it, so catalog updates, advisories and delisting do not apply
  to it. That is what you want while developing. Publishing to the catalog is a separate path.

## Rules that fail silently when broken

These are the ones where the failure has **no error message** — which is what makes them rules
rather than advice.

- **A field that is not in the contract never reaches the implementation.** Adding an input to
  the handler without declaring it looks like "I passed it and nothing happened". The reverse —
  declaring a field the implementation never produces — puts a permanently empty variable on
  the canvas. Align them one by one.
- **Bind with `contract.BindInput`, not `json.Unmarshal`.** It walks `sokel` tags
  recursively; `json.Unmarshal` only knows json tags and Go field names, so a camelCase
  contract name silently fails to land in its field and the handler quietly takes the default
  branch. Hand-written nested types need the `sokel` tag too.
- **Optional numbers must be pointers.** A value type turns "not set" into `0` and sends it
  upstream — silently changing behaviour with nothing to see.
- **Typed outputs flatten every field.** Nil pointers and interfaces are dropped, but empty
  strings are not: "nothing this time" should not show up as an empty value downstream.
- **Streaming: the delta and the accumulated text need different fields.** Platform semantics
  are "a later frame overwrites the same field", so sharing one field leaves the consumer with
  the last fragment only.
- **Opaque fields require a stated reason** (the argument is mandatory — it will not compile
  without one). Most "it has no structure" turns out to be structure nobody wrote down.
- **Event common fields are listed explicitly, never intersected.** If they were inferred, one
  new event missing one field would silently shrink the common set and break live workflows.
- **Credentials hold identity; deployment configuration goes in environment variables.** The
  test: would this value still be true for the same plugin deployed on another machine? If not,
  it is environment, not credential.
- **An optional secret is injected only when it has a value.** Exporting an empty value can
  wipe a login already present in the environment.
- **Set the operation timeout.** The platform default is 60s; long work (transcription, long
  generation) gets cut in half, and the person dragging the node onto the canvas has no idea
  what to type. The plugin knows how long it takes.
- **Error text is shown to users.** "missing access token (set it in the plugin's credentials,
  scope `api` or wider)" — not "unauthorized".

## Verify against the real upstream, not the documentation

The most expensive rule here. One real call to a real instance routinely exposes things no
amount of reading finds — an id field that means something else, two event kinds that are
actually one discriminated by a type field, machine-generated records mixed into what looks
like human content. Each of those routes a trigger onto the wrong object or burns a downstream
run.

**Test fixtures must be payloads captured from the real service.** A fixture you wrote yourself
grows to match your understanding of the API, which is exactly why it will always be green.

Assert on **the bytes actually sent** (record them with a fake upstream) rather than on what the
code constructed — especially when an official SDK builds the request, since your structs are
then no longer the authority. Give the fake upstream a real `Content-Type`; official SDKs check it
even when hand-written clients do not. When a plugin drives an external CLI, verify flags against
`--help` on the real version and pin them in a test.

Ways a test can be green while the thing is broken: a fixture you invented; asserting on an AI's
wording instead of on structure; a fixture missing a whole class of input, so that branch never
runs; grepping source text; asserting that a replacement "happened" without asserting it matched.

## Every plugin ships two documents

`README.md` for whoever changes the code (design decisions, upstream quirks) and
`docs/<plugin>.md` for whoever uses it (how to configure it, what it can do). Both, always —
they address different readers and neither substitutes for the other.
