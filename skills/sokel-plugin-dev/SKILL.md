---
name: sokel-plugin-dev
description: >-
  Write, change, port or review a Sokel plugin — a process that dials back into a
  Sokel platform and exposes typed operations, events, webhooks and credentials.
  Use it for "write a plugin for <service>", "add an operation / event / webhook",
  "why doesn't the platform see my field", "port this plugin to Python/TypeScript/Go",
  installing a plugin onto a platform, or reviewing plugin code. Covers the toolchain,
  the manifest format, the declare-then-implement order, and the rules that fail
  silently when broken.
---

# Writing a Sokel plugin

A plugin is one process. It **dials out** to the platform (no inbound port, no public IP), reports a
**contract** — which operations it has, what each takes and returns, which events it can emit, what
its credential form looks like — and then answers calls.

Three properties shape everything else:

- **The contract is declared, never reflected at runtime**, and declared *before* it is implemented.
  It is a public interface: reviewable on its own, and it is what the platform renders on a canvas.
- **The contract is language-neutral.** Go, Python and TypeScript plugins report the same JSON; the
  platform neither knows nor needs to know which one you used.
- **Codegen, not a framework.** You declare; `sokel-gen` renders a typed shell; you implement
  handlers whose signatures are fully concrete. A declaration that changes breaks compilation rather
  than some later call.

## Start here

| If you are | Read |
|---|---|
| installing the toolchain, or looking for a command | [`references/toolchain.md`](references/toolchain.md) |
| writing the contract | [`references/manifest.md`](references/manifest.md) — the full format guide |
| looking for how to express a shape | [`references/example.manifest.yml`](references/example.manifest.yml) — every shape, once each |
| wiring editor completion | [`references/manifest.schema.json`](references/manifest.schema.json) |
| implementing in Python / TypeScript | [`references/example.python.py`](references/example.python.py) · [`references/example.node.ts`](references/example.node.ts) |
| getting it onto a platform | [`references/platform.md`](references/platform.md) |
| reviewing, or about to commit | [`references/rules.md`](references/rules.md) — **read this one** |

The format guide, the schema and the reference declaration are also inside the binary
(`sokel-gen docs`, `sokel-gen docs schema`, `sokel-gen example`) — the same files, so there is never
a stale second copy. The ones here are for reading before the toolchain is installed.

## Declaring the contract: two entry points, neither privileged

| You write | Generate with | Why you would |
|---|---|---|
| `manifest.yml` | `sokel-gen generate -lang go\|python\|ts <dir>` | One language-neutral file, the same one distribution wants. Available in **all three languages** |
| a `schema/` package (Go only) | `sokel-gen generate <dir>` | The contract is executable Go: a misspelled method is a compile error, and existing Go types are reused in place |

Both produce the same contract, and in Go both produce the **same generated API** (`OnXxx`,
`RegisterCredential`, `DeclareEvents`, `TriggerXxx`), so an implementation cannot tell which route
produced it. `sokel-gen export yaml` converts a `schema/` package into a manifest.

**Default to the manifest** unless you have a reason: it is one file, it is what gets published, and
a plugin ported between languages keeps its declaration instead of having it retyped.

## The order of work

1. **Scaffold** — `sokel-gen init ./my-plugin -lang go|python|ts` (add `-manifest` for a Go plugin
   declared in a manifest). What comes out runs end to end already; the `hello` operation in it is
   real, not a placeholder. Starting from something that runs saves a round of guessing.
2. **Declare** the contract. Declaration only, no implementation.
3. **Generate** — `sokel-gen generate <dir>`. It reports every problem in one pass, so fix them as a
   batch. **Never hand-edit generated files**; the next run overwrites them and CI catches it.
4. **Implement** the handlers. No `any`, no generics — the types are the ones you declared.
5. **Install onto a platform and run it** — [`references/platform.md`](references/platform.md). This
   is where the access token comes from.
6. **Exercise one operation** from the platform's debug console. It is not a simulation: a write
   operation really writes.
7. **Before committing** — `sokel-gen check <dir>`, build, vet, test, and both documents (below).

## What ships with a plugin

| Part | Where |
|---|---|
| Contract | `manifest.yml`, or a `schema/` package |
| Generated shell | `zz_*.go` / `sokel_gen.py` / `sokel.gen.ts` — **never edited by hand** |
| Implementation | `main.go` / `main.py` / `src/main.ts` and friends |
| Two documents | `README.md` for whoever changes the code; `docs/<plugin>.md` for whoever uses it. **Both, always** — different readers, neither substitutes for the other |

## The part that bites

Most plugin bugs are not crashes. They are a field that never arrives, a number that silently became
zero, a trigger that fired on the wrong object — each with no error anywhere. Those are collected in
[`references/rules.md`](references/rules.md), along with why a test fixture you wrote yourself will
always be green. Read it before reviewing or shipping; it is short, and every line in it is
something that already shipped broken once.
