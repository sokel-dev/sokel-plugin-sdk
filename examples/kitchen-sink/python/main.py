# Copyright 2026 The Sokel Authors
# SPDX-License-Identifier: Apache-2.0

"""The kitchen-sink reference plugin (Python implementation).

The contract is declared in ../sokel.yaml and sokel_gen.py is generated from it, so this file is
only the implementation — typed throughout. The Node version implements the **same declaration**
(../node/src/main.ts), and both must report a byte-identical contract.

Run it:

    uv pip install -r requirements.txt
    SOKEL_ENDPOINT=http://localhost:8088 SOKEL_TOKEN=skp_xxx python main.py
"""

from __future__ import annotations

import asyncio
import hashlib
import json
import logging
import time
from typing import Dict

from sokel import AuthChallenge, AuthState, Ctx, Emitter, SourceCtx, WebhookRequest, WebhookResponse, ok, text

from sokel_gen import (
    ChatStreamIn,
    ChatStreamOut,
    HealthCheckIn,
    HealthCheckOut,
    DocObject,
    EchoAllIn,
    EchoAllOut,
    FileDigestIn,
    FileDigestOut,
    HeartbeatEvent,
    MessageEvent,
    credential,
    new_plugin,
    on_chat_stream,
    on_echo_all,
    on_file_digest,
    on_health_check,
    trigger_heartbeat,
    trigger_message,
)

logging.basicConfig(level=logging.INFO, format="%(asctime)s %(message)s")

p = new_plugin(name="kitchen-sink", version="1.0.0")


# —— operation: echo every field shape back ——


async def echo_all(ctx: Ctx, in_: EchoAllIn) -> EchoAllOut:
    cred = credential(ctx)  # typed credential read; a misspelled ctx.credential["api_key"] binds nothing
    # Structural union: the runtime value *is* the branch (no discriminator), so a type check is enough
    if isinstance(in_.doc, DocObject):
        doc_desc = f"document object {in_.doc.title!r}"
    elif isinstance(in_.doc, list):
        doc_desc = f"block array ({len(in_.doc)} blocks)"
    else:
        doc_desc = "no document"
    return EchoAllOut(
        text=in_.text * max(in_.count, 1),
        count=in_.count,
        mode=in_.mode,
        tags=in_.tags,
        profile=in_.profile,
        points=in_.points,
        labels=in_.labels,
        summary=(
            f"mode={in_.mode} ratio={in_.ratio} flag={in_.flag} "
            f"tags={len(in_.tags)} points={len(in_.points)} extra_keys={sorted(in_.extra)} "
            f"{doc_desc} region={cred.region}"
        ),
    )


on_echo_all(p, echo_all)


# —— operation: a file input fetched lazily, a file output handed back to the platform ——


async def file_digest(ctx: Ctx, in_: FileDigestIn) -> FileDigestOut:
    data = await ctx.fetch(in_.file)
    digest = hashlib.sha256(data).hexdigest()
    report = await ctx.upload(
        "digest.json",
        "application/json",
        json.dumps({"name": in_.file.name, "sha256": digest, "size": len(data)}, ensure_ascii=False).encode(),
    )
    return FileDigestOut(
        name=in_.file.name,
        sha256=digest,
        size=len(data),
        extra_count=len(in_.extras),
        report=report,
    )


on_file_digest(p, file_digest)


# —— operation: streaming ——


async def chat_stream(ctx: Ctx, in_: ChatStreamIn, out: Emitter) -> None:
    frames = max(in_.chunks, 1)
    reply = ""
    for i in range(frames):
        piece = f"{in_.prompt}#{i + 1} "
        reply += piece
        out.text(piece)  # human-readable increment: visible live while the node runs
        await asyncio.sleep(0.05)
    # Finish with typed variables: they flow downstream and are checked against the Outputs contract
    out.vars(ChatStreamOut(reply=reply.strip(), frames=frames))


on_chat_stream(p, chat_stream)


# —— credential check: the platform's conventional id, called by the credential page's "Test" ——


def health_check(ctx: Ctx, _in: HealthCheckIn) -> HealthCheckOut:
    cred = credential(ctx)
    # An unusable credential must come back as **ok=false, not as an error**: an error leaves the
    # platform able to say only "the call failed", not whether the key is missing or upstream said
    # no — and those two call for completely different fixes.
    if not cred.api_key:
        return HealthCheckOut(ok=False, message="the credential has no API key")
    if not cred.api_key.startswith("sk-"):
        return HealthCheckOut(ok=False, message=f"an API key looks like sk-…, this one is {cred.api_key[:6]!r}…")
    # A real plugin makes its cheapest upstream call here (GET /me, say) and passes the reply through
    return HealthCheckOut(ok=True, message=f"{cred.base_url} / {cred.region} reachable")


on_health_check(p, health_check)


# —— webhook: verify the signature, then push a typed event ——


def handle_webhook(ctx: SourceCtx, req: WebhookRequest) -> WebhookResponse:
    cred = credential(ctx)   # works the same on a source ctx and an operation ctx
    # Verifying the upstream signature is **the plugin's job**: every vendor signs differently, and
    # the platform does not know the upstream — the plugin does
    if req.header("X-Sokel-Token") != cred.api_key:
        return text(401, "bad token")
    body = json.loads(req.body or b"{}")
    asyncio.get_running_loop().create_task(
        trigger_message(
            ctx,
            str(body.get("id") or ""),  # idempotency key: an upstream retry triggers once
            MessageEvent(chat_id=str(body.get("chat_id") or ""), text=str(body.get("text") or "")),
        )
    )
    return ok()


p.register_webhook(handle_webhook)


# —— long-running event source: one heartbeat every 30 seconds ——


async def heartbeat(ctx: SourceCtx) -> None:
    cred = credential(ctx)
    if not cred.api_key:
        # With no credential, report "needs login" so the panel lights that row up instead of
        # sitting there quietly doing nothing
        ctx.report_status("auth_required", "the credential has not logged in yet")
        return
    while not ctx.stopping.is_set():
        await trigger_heartbeat(
            ctx,
            f"hb-{int(time.time())}",
            HeartbeatEvent(chat_id="system", at=time.strftime("%Y-%m-%dT%H:%M:%SZ", time.gmtime())),
        )
        try:
            await asyncio.wait_for(ctx.stopping.wait(), timeout=30)
        except asyncio.TimeoutError:
            continue


p.register_source("heartbeat", "Heartbeat", heartbeat)


# —— collaborative authentication (kind=input: start -> poll -> submit) ——

_pending: Dict[str, str] = {}  # auth_id -> the submitted code; use expiring storage in production


def auth_start(ctx: Ctx) -> AuthChallenge:
    auth_id = f"demo-{int(time.time())}"
    _pending[auth_id] = ""
    return AuthChallenge(auth_id=auth_id, prompt="Type any six digits as the verification code", expires_in=300)


def auth_submit(ctx: Ctx, auth_id: str, user_input: str) -> None:
    if len(user_input) != 6 or not user_input.isdigit():
        raise ValueError("the code must be six digits")
    _pending[auth_id] = user_input


def auth_poll(ctx: Ctx, auth_id: str) -> AuthState:
    code = _pending.get(auth_id)
    if not code:
        return AuthState(status="pending")
    # Only a confirmed state carries the session: handing it over earlier makes the platform rewrite
    # the credential row over and over
    return AuthState(status="confirmed", session={"api_key": f"sk-demo-{code}"})


p.register_auth(start=auth_start, poll=auth_poll, submit=auth_submit)


if __name__ == "__main__":
    asyncio.run(p.run())
