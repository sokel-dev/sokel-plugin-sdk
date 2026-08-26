# Sokel Plugin SDK — Python

[简体中文](README.zh-CN.md)

Write [Sokel](https://github.com/sokel-dev) plugins in Python. The contract lives in a
language-neutral `sokel.yaml`; `sokel-gen` turns it into pydantic models and typed registration
functions, and the SDK handles registration, transport, credentials, files, heartbeats and
reconnects.

```python
async def issues_list(ctx: Ctx, in_: IssuesListIn) -> IssuesListOut:
    issues = await client.list_issues(in_.project, in_.state)
    return IssuesListOut(issues=issues, count=len(issues))

on_issues_list(p, issues_list)
```

A typo in `in_.project` is a red squiggle in your editor, not a failed call in production — there is
no `dict["key"]` anywhere in your code.

## Install

```bash
pip install sokel-plugin-sdk
go install github.com/sokel-dev/sokel-plugin-sdk/cmd/sokel-gen@latest   # the generator
```

`sokel-gen` is a single binary (written in Go) used **only at generation time**; running a plugin
does not need it.

## Four steps

```bash
sokel-gen init -lang python ./my-plugin
cd my-plugin
pip install -r requirements.txt
sokel-gen generate .     # sokel.yaml → sokel_gen.py
python main.py
```

1. **Declare** — `sokel.yaml`: operations, events, credentials, authentication. Format:
   [docs/manifest.md](../docs/manifest.md), or run `sokel-gen docs`.
2. **Generate** — `sokel-gen generate .` writes `sokel_gen.py`: an `XxxIn` / `XxxOut` model pair and
   an `on_xxx(p, fn)` per operation; a payload model and a `trigger_xxx(ctx, event_id, payload)` per
   event.
3. **Implement** — handler signatures are fully concrete. `async def` or a plain function, both work.
4. **Connect** — `asyncio.run(p.run())`. A plugin **dials out**: no inbound port, no public IP, no
   firewall hole.

## What you can do

| Task | How |
|---|---|
| Read credentials | `credential(ctx)` → the generated `Credential` model |
| Read an input file's bytes | `await ctx.fetch(in_.file)` |
| Produce a file | `await ctx.upload(name, mime, data)`, or `await ctx.upload_file(path)` for large files |
| Stream output | `out.text(...)` frame by frame for humans, `out.vars(Out(...))` for downstream nodes |
| Push an event | `await trigger_message(ctx, event_id, MessageEvent(...))` |
| Long-running event source | `p.register_source(id, label, fn)`; loop while `not ctx.stopping.is_set()` |
| Handle a platform-relayed webhook | `p.register_webhook(fn)`, return `ok()` / `text(401, "...")` |
| Collaborative authentication | `p.register_auth(start=…, poll=…, submit=…)` |
| Refresh a session credential | `await ctx.update_credential({"session": "…"})` |
| Report runtime state | `ctx.report_status("auth_required", "…")` |

`upload_file(path)` streams from disk: memory stays at one chunk (1 MiB) regardless of file size.
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
streaming, events, webhooks, collaborative auth — and the Python and Node implementations share one
declaration.

```bash
cd examples/kitchen-sink/python
pip install -r requirements.txt
SOKEL_ENDPOINT=nats://localhost:4222 SOKEL_TOKEN=skp_xxx python main.py
```

## Developing this SDK

```bash
uv venv && uv pip install -e '.[dev]'
python -m pytest -q
```

## License

Apache-2.0.
