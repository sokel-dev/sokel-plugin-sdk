# Sokel Plugin SDK — Node.js / TypeScript

[简体中文](README.zh-CN.md)

Write [Sokel](https://github.com/sokel-dev) plugins in TypeScript. The contract lives in a
language-neutral `manifest.yml`; `sokel-gen` turns it into TypeScript interfaces and typed
registration functions, and the SDK handles registration, transport, credentials, files, heartbeats
and reconnects.

```ts
onIssuesList(p, async (ctx, in_) => {
  const issues = await client.listIssues(in_.project, in_.state);
  return { issues, count: issues.length };
});
```

A typo in `in_.project` is a compile error, not a failed call in production — there is no `any`
anywhere in your code.

## Install

```bash
npm install @sokel-dev/plugin-sdk
go install github.com/sokel-dev/sokel-plugin-sdk/cmd/sokel-gen@latest   # the generator
```

`sokel-gen` is a single binary (written in Go) used **only at generation time**; running a plugin
does not need it.

## Four steps

```bash
sokel-gen init -lang ts ./my-plugin
cd my-plugin && npm install
sokel-gen generate .     # manifest.yml → src/sokel.gen.ts
npm run build && npm start
```

1. **Declare** — `manifest.yml`: operations, events, credentials, authentication. Format:
   [docs/manifest.md](../docs/manifest.md), or run `sokel-gen docs`.
2. **Generate** — `sokel-gen generate .` writes `src/sokel.gen.ts`: an `XxxIn` / `XxxOut` interface
   pair and an `onXxx(p, fn)` per operation; a payload interface and a
   `triggerXxx(ctx, eventId, payload)` per event.
3. **Implement** — handler signatures are fully concrete; return a value or a promise.
4. **Connect** — `await p.run()`. A plugin **dials out**: no inbound port, no public IP, no firewall
   hole.

## Why not zod

A zod schema is a runtime object, so declaring the contract in zod means the contract only exists
once the process is running — and every language would have to interpret that DSL for itself. The
declaration stays in `manifest.yml`; TypeScript takes only the types, which is what TypeScript is good
at: compile-time checks, zero runtime cost.

## What you can do

| Task | How |
|---|---|
| Read credentials | `ctx.credentialAs<Credential>()` |
| Read an input file's bytes | `await ctx.fetch(in_.file)` |
| Produce a file | `await ctx.upload(name, mime, bytes)`, or `await ctx.uploadFile(path)` for large files |
| Stream output | `out.text(...)` frame by frame for humans, `out.vars({...})` for downstream nodes |
| Push an event | `await triggerMessage(ctx, eventId, {...})` |
| Long-running event source | `p.registerSource(id, label, fn)`; loop while `!ctx.stopped` |
| Handle a platform-relayed webhook | `p.registerWebhook(fn)`, return `ok()` / `text(401, "...")` |
| Collaborative authentication | `p.registerAuth({ start, poll, submit })` |
| Refresh a session credential | `await ctx.updateCredential({ session: "…" })` |
| Report runtime state | `ctx.reportStatus("auth_required", "…")` |

`uploadFile(path)` streams from disk: memory stays at one chunk (1 MiB) regardless of file size.
Anything above a few hundred megabytes should use it — `upload(bytes)` reads the whole file into
memory first, and the symptom of that is a container mysteriously killed by the OOM reaper.

## Configuration

The SDK reads `SOKEL_`-prefixed environment variables:

| Variable | Required | Meaning |
|---|---|---|
| `SOKEL_ENDPOINT` | yes | `nats://broker:4222`, or an `https://` platform URL to discover the broker from |
| `SOKEL_TOKEN` | yes | Access-group token (`skp_…`) identifying plugin + workspace |
| `SOKEL_NATS_TOKEN` | no | Broker-level auth |
| `SOKEL_NATS_CA` | no | Custom CA bundle for `tls://` brokers |
| `SOKEL_INSTANCE_ID` | no | Pin a replica identity (otherwise derived from the token and cached on disk) |
| `SOKEL_REGION` | no | Region label for the replica |

Credentials are never stored by the plugin: the platform injects the resolved fields with each call.

## Example

[`examples/kitchen-sink`](../examples/kitchen-sink) covers every shape — each field type, files,
streaming, events, webhooks, collaborative auth — and the Node and Python implementations share one
declaration.

```bash
cd examples/kitchen-sink/node
npm install && npm run build
SOKEL_ENDPOINT=nats://localhost:4222 SOKEL_TOKEN=skp_xxx npm start
```

## Developing this SDK

```bash
pnpm install
pnpm test        # tsc + node --test
```

## License

Apache-2.0.
