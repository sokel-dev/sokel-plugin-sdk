# Copyright 2026 The Sokel Authors
# SPDX-License-Identifier: Apache-2.0

"""Ephemeral, group-scoped transport for frames above the broker's message budget."""
from __future__ import annotations

import asyncio
import hashlib
import json
import re
import secrets
import time
from typing import Any, Optional

from .errors import PluginError

MAX_BYTES = 512 * 1024 * 1024
MAX_CHUNK = 1024 * 1024
PAYLOAD_TIMEOUT = 120


class PayloadError(PluginError):
    def __init__(self, message: str, code: str = "payload_invalid") -> None:
        super().__init__(message)
        self.code = code


def payload_group(subject: str) -> str:
    parts = subject.split(".")
    if parts[-1] == "large":
        parts.pop()
    group = parts[-1] if parts else ""
    if not re.fullmatch(r"[A-Za-z0-9_-]+", group):
        raise PayloadError("invalid payload group")
    return group


def _remaining(deadline: float) -> float:
    seconds = deadline - time.monotonic()
    if seconds <= 0:
        raise PayloadError("payload transfer deadline exceeded", "payload_timeout")
    return seconds


async def fetch_payload(nc: Any, group: str, body: bytes, deadline: Optional[float] = None) -> bytes:
    """Only the dedicated .large operation subject treats its body as a reference."""
    deadline = deadline if deadline is not None else time.monotonic() + PAYLOAD_TIMEOUT
    try:
        outer = json.loads(body)
    except (ValueError, UnicodeDecodeError):
        raise PayloadError("invalid payload reference JSON") from None
    r = outer.get("__sokel_payload") if isinstance(outer, dict) else None
    prefix = f"sokel.payload.{group}."
    if (not isinstance(outer, dict) or len(outer) != 1 or not isinstance(r, dict)
            or type(r.get("version")) is not int or r["version"] != 1
            or not isinstance(r.get("subject"), str) or not r["subject"].startswith(prefix)
            or not re.fullmatch(r"[A-Za-z0-9_-]+", r["subject"][len(prefix):])
            or type(r.get("size")) is not int or not 0 <= r["size"] <= MAX_BYTES
            or type(r.get("chunk_bytes")) is not int or not 1 <= r["chunk_bytes"] <= MAX_CHUNK
            or not isinstance(r.get("sha256"), str) or not re.fullmatch(r"[a-f0-9]{64}", r["sha256"])):
        raise PayloadError("invalid payload reference")
    try:
        chunks = []
        offset = 0
        digest = hashlib.sha256()
        while offset < r["size"]:
            response = await nc.request(r["subject"], json.dumps({"offset": offset}).encode(), timeout=_remaining(deadline))
            if len(response.data) != min(r["chunk_bytes"], r["size"] - offset):
                raise PayloadError("payload chunk length mismatch")
            chunks.append(response.data)
            digest.update(response.data)
            offset += len(response.data)
        if digest.hexdigest() != r["sha256"]:
            raise PayloadError("payload checksum mismatch")
        return b"".join(chunks)
    except BaseException:
        # The call's deadline may have expired; cancellation gets a fresh short timeout.
        try:
            await nc.request(r["subject"], b'{"cancel":true}', timeout=1)
        except Exception:
            pass
        raise


async def publish_payload(nc: Any, group: str, reply: str, data: bytes, supported: bool,
                          headers: Any = None, deadline: Optional[float] = None) -> None:
    """Keep the producer alive until the receiver has pulled the final chunk."""
    budget = max(0, getattr(nc, "max_payload", MAX_CHUNK) - 1024)
    if len(data) <= budget:
        await nc.publish(reply, data, headers=headers)
        return
    if not supported or len(data) > MAX_BYTES or budget < 1:
        raise PayloadError(f"reply exceeds the transport limit ({len(data)} bytes); upgrade the platform for chunked payload support", "payload_too_large")
    deadline = deadline if deadline is not None else time.monotonic() + PAYLOAD_TIMEOUT
    _remaining(deadline)
    subject = f"sokel.payload.{group}.{secrets.token_hex(16)}"
    chunk_bytes = min(MAX_CHUNK, budget)
    envelope = json.dumps({"__sokel_payload": {"version": 1, "subject": subject, "size": len(data),
                           "sha256": hashlib.sha256(data).hexdigest(), "chunk_bytes": chunk_bytes}}).encode()
    if len(envelope) > budget:
        raise PayloadError("broker payload limit cannot fit a transfer reference", "payload_too_large")
    done = asyncio.get_running_loop().create_future()
    offset = 0

    async def on_chunk(msg):
        nonlocal offset
        if done.done():
            return
        try:
            request = json.loads(msg.data)
            if request.get("cancel") is True:
                await msg.respond(b"")
                raise PayloadError("payload transfer cancelled", "payload_cancelled")
            if type(request.get("offset")) is not int or request["offset"] != offset:
                await msg.respond(b"")
                raise PayloadError("invalid payload chunk offset")
            chunk = data[offset:offset + chunk_bytes]
            await msg.respond(chunk)
            offset += len(chunk)
            if offset == len(data):
                done.set_result(None)
        except Exception as error:
            if not done.done():
                done.set_exception(error)

    sub = await nc.subscribe(subject, cb=on_chunk)
    try:
        await asyncio.wait_for(nc.flush(), timeout=_remaining(deadline))
        await nc.publish(reply, envelope, headers={**(headers or {}), "Sokel-Payload": "1"})
        try:
            await asyncio.wait_for(done, timeout=_remaining(deadline))
        except asyncio.TimeoutError:
            raise PayloadError("payload transfer deadline exceeded", "payload_timeout") from None
    finally:
        await sub.unsubscribe()
        # Observe a producer failure even when publish/flush failed before we awaited it.
        if done.done() and not done.cancelled():
            done.exception()
        else:
            done.cancel()
