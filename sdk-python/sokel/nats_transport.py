# Copyright 2026 The Sokel Authors
# SPDX-License-Identifier: Apache-2.0

"""The NATS transport: the plugin **dials out** to the broker (no inbound port, no public IP, no
firewall hole).

The flow matches the Go SDK step for step (protocol §1-§7): discover, connect, register (reporting
the contract), subscribe (as a queue group), renew by heartbeat, dispatch calls, shut down
gracefully. Event-source plugins additionally run a per-credential supervisor.
"""

from __future__ import annotations

import asyncio
import base64
import io
import json
import logging
import os
import signal
import socket
import time
from typing import Any, Dict, List, Optional, Tuple

import nats
from nats.aio.msg import Msg

from . import contract as C
from . import env
from .events import CredEntry, SourceCtx, desired_source_creds, SourceSupervisor
from .plugin import Plugin
from .runtime import BufferSink, File

log = logging.getLogger("sokel")

QUEUE_GROUP = "sokel-workers"  # replicas of a group share one queue: each call goes to exactly one
INSTANCE_HEADER = "Sokel-Instance"  # replies and stream frames report which replica answered
FILE_CHUNK = 1 << 20  # 1 MiB. Bytes never ride the operation reply (max_payload); they go through
# the dedicated chunk channel instead.
HEARTBEAT_SEC = 20
RETRY_SEC = 8
REQUEST_TIMEOUT = 8
FILE_TIMEOUT = 30


class NatsFiles:
    """Exchange file bytes with the platform over the same NATS connection. The plugin never needs
    HTTP access to the platform, so a plugin behind NAT works the same way."""

    def __init__(self, nc: Any, token: str) -> None:
        self._nc = nc
        self._token = token

    async def fetch(self, f: File) -> bytes:
        fid = f.id or (f.url.rsplit("/", 1)[-1] if f.url else "")
        if not fid:
            raise ValueError("the file reference has neither id nor url")
        out = bytearray()
        seq = 0
        while True:
            req = json.dumps({"token": self._token, "id": fid, "seq": seq}).encode()
            resp = await self._nc.request("sokel.file.get", req, timeout=FILE_TIMEOUT)
            r = json.loads(resp.data)
            if r.get("error"):
                raise RuntimeError(r["error"])
            out += base64.b64decode(r.get("data") or "")
            if r.get("last"):
                return bytes(out)
            seq += 1

    async def store(self, name: str, mime: str, data: bytes) -> File:
        """Whole bytes. Delegates to store_stream: the chunking protocol should have one implementation."""
        return await self.store_stream(name, mime, io.BytesIO(data))

    async def store_stream(self, name: str, mime: str, src: Any) -> File:
        """**Stream while reading**; memory stays at one chunk.

        The platform already writes into blob storage chunk by chunk — the bottleneck was only ever
        on the plugin side.
        """
        upload_id = ""
        seq = 0
        while True:
            chunk = src.read(FILE_CHUNK)
            # A short read means EOF. An empty file still makes one round (last=True, 0 bytes),
            # otherwise the platform has no session to close out.
            last = len(chunk) < FILE_CHUNK
            req = json.dumps(
                {
                    "token": self._token,
                    "upload_id": upload_id,
                    "name": name,
                    "mime": mime,
                    "seq": seq,
                    "last": last,
                    "data": base64.b64encode(chunk).decode(),
                }
            ).encode()
            resp = await self._nc.request("sokel.file.put", req, timeout=FILE_TIMEOUT)
            r = json.loads(resp.data)
            if r.get("error"):
                raise RuntimeError(r["error"])
            if r.get("upload_id"):
                upload_id = r["upload_id"]
            if last:
                f = r.get("file")
                if not f:
                    raise RuntimeError("the platform returned no file reference")
                return File(**f)
            seq += 1


class NatsTransport:
    async def run(self, p: Plugin) -> None:
        acc = await discover(p.endpoint, p.token)
        target = acc["url"]
        opts: Dict[str, Any] = {
            "servers": [target],
            "name": p.name,
            "max_reconnect_attempts": -1,  # reconnect forever; subscriptions restore themselves
            "reconnect_time_wait": 2,
            "disconnected_cb": _cb(lambda: log.warning("[sokel] disconnected from the platform (reconnecting)")),
            "reconnected_cb": _cb(lambda: log.info("[sokel] reconnected to the platform")),
        }
        # The broker authorizes per access group: connect with this group's own credentials,
        # handed out by the platform. The legacy shared token remains only as a fallback for
        # endpoints that skipped discovery (a literal nats:// URL).
        if acc.get("user"):
            opts["user"], opts["password"] = acc["user"], acc["pass"]
        elif tok := (env.get("NATS_TOKEN") or p.token):
            opts["token"] = tok
        # Trusting the broker's certificate, in order of preference: the CA the platform shipped
        # with the credentials (nothing to configure), then SOKEL_NATS_CA (a local file). A broker
        # reachable only by IP cannot get a publicly trusted certificate, so self-signed is the
        # norm there -- without the shipped CA every replica host needs the same file by hand.
        import ssl

        if ca_pem := acc.get("ca"):
            ctx = ssl.create_default_context()
            ctx.load_verify_locations(cadata=ca_pem)
            opts["tls"] = ctx
        elif ca := env.get("NATS_CA"):
            opts["tls"] = ssl.create_default_context(cafile=ca)
        nc = await _connect_forever(opts)

        host = socket.gethostname()
        instance_id = stable_instance_id(p.token)
        started_at = time.strftime("%Y-%m-%dT%H:%M:%SZ", time.gmtime())
        files = NatsFiles(nc, p.token)
        state: Dict[str, Any] = {"notify": ""}

        async def register() -> Tuple[str, str, List[CredEntry]]:
            body = p.register_payload(instance_id, host, started_at)
            resp = await nc.request("sokel.register", json.dumps(body).encode(), timeout=REQUEST_TIMEOUT)
            reg = json.loads(resp.data)
            if not reg.get("ok") or not reg.get("subject"):
                raise RuntimeError(f"registration refused: {reg.get('error') or 'the platform returned no subject'}")
            if reg.get("notify_subject"):
                state["notify"] = reg["notify_subject"]
            creds = [CredEntry(c.get("id", ""), c.get("fields")) for c in (reg.get("credentials") or [])]
            # An older platform only sends the singular form; fold it into a one-element set.
            if not creds and (reg.get("credential_id") or reg.get("credential")):
                creds = [CredEntry(reg.get("credential_id", ""), reg.get("credential"))]
            return reg["subject"], reg.get("name") or p.name, creds

        # A failed first registration is not fatal (broker or platform may still be starting):
        # retry at a fixed interval until it succeeds.
        while True:
            try:
                subject, name, creds = await register()
                break
            except Exception as e:  # noqa: BLE001
                log.warning("[sokel] registration failed (%s), retrying in %ds…", e, RETRY_SEC)
                await asyncio.sleep(RETRY_SEC)

        async def on_call(msg: Msg) -> None:
            await self._dispatch(p, nc, msg, files, instance_id)

        await nc.subscribe(subject, queue=QUEUE_GROUP, cb=on_call)
        log.info("[sokel] connected: plugin %r ready, replica %s listening on %s", name, instance_id, subject)

        supervisor: Optional[SourceSupervisor] = None
        if p.sources:
            supervisor = _make_supervisor(p, nc, files)
            supervisor.reconcile(desired_source_creds(creds))
            if state["notify"]:
                # Credential-change notifications use a plain subscription (not a queue group):
                # every replica in the group must hear it, since any assignment may have changed.
                debounce = _Debouncer(0.3, lambda: _resync(register, supervisor))

                async def on_notify(_msg: Msg) -> None:
                    debounce.trigger()

                # The callback must be a coroutine: nats-py rejects a plain function outright with
                # "must use coroutine for subscriptions".
                await nc.subscribe(state["notify"], cb=on_notify)

        stop = asyncio.Event()
        loop = asyncio.get_running_loop()
        for sig in (signal.SIGINT, signal.SIGTERM):
            try:
                loop.add_signal_handler(sig, stop.set)
            except NotImplementedError:  # Windows, or not the main thread
                pass

        try:
            while not stop.is_set():
                try:
                    await asyncio.wait_for(stop.wait(), timeout=HEARTBEAT_SEC)
                    break
                except asyncio.TimeoutError:
                    pass
                try:
                    _, _, hb_creds = await register()
                    if supervisor is not None:
                        # Reconcile against the latest assignment each tick: shard moves,
                        # credentials added or removed, fields refreshed.
                        supervisor.reconcile(desired_source_creds(hb_creds))
                except Exception as e:  # noqa: BLE001
                    log.warning("[sokel] heartbeat renewal failed: %s", e)
        finally:
            # Graceful shutdown: tell the platform to mark this replica offline right away
            # (seconds), instead of waiting for the heartbeat sweep (45s+).
            if supervisor is not None:
                supervisor.stop_all()
            try:
                await nc.publish(
                    "sokel.unregister",
                    json.dumps({"token": p.token, "instance_id": instance_id}).encode(),
                )
                await nc.flush()  # make sure the goodbye lands before the connection drops
            except Exception:  # noqa: BLE001
                pass
            log.info("[sokel] told the platform we are going offline, exiting")
            await nc.drain()

    async def _dispatch(self, p: Plugin, nc: Any, msg: Msg, files: NatsFiles, instance_id: str) -> None:
        if not msg.reply:
            return
        try:
            call = json.loads(msg.data)
        except Exception:  # noqa: BLE001
            await nc.publish(msg.reply, json.dumps({"error": "could not parse the call frame"}).encode())
            return
        op = call.get("operation") or ""
        tag = _trace_tag(call.get("trace") or {})

        # The platform-relayed webhook frame is intercepted before dispatch. An older SDK without
        # this branch falls through to "unknown operation", which the platform translates into
        # "the plugin registered no webhook handler".
        if op == C.OP_WEBHOOK:
            log.info("[sokel] ← webhook inbound%s", tag)
            sctx = SourceCtx(
                token=p.token,
                publish=nc.publish,
                valid_events=p.contract.event_ids(),
                credential=call.get("credential"),
                credential_id=call.get("credential_id", ""),
                source_id="webhook",
                files=files,
            )
            out = await p.handle_webhook(sctx, call.get("input") or {})
            await nc.publish(msg.reply, json.dumps(out).encode())
            return

        headers = {INSTANCE_HEADER: instance_id}
        started = time.monotonic()
        log.info("[sokel] ← %s started%s", op, tag)
        if p.contract.is_stream(op):
            # Streaming: publish frame by frame to the reply subject; the end frame is mandatory.
            async def publish_frame(frame: Dict[str, Any]) -> None:
                await nc.publish(msg.reply, json.dumps(frame).encode(), headers=headers)

            pending: List[asyncio.Task] = []

            def sink(frame: Dict[str, Any]) -> None:
                pending.append(asyncio.create_task(publish_frame(frame)))

            try:
                await p.dispatch(call, sink, files)
                if pending:
                    await asyncio.gather(*pending)
                log.info("[sokel] ✓ %s done (%dms)%s", op, _ms(started), tag)
            except Exception as e:  # noqa: BLE001
                log.warning("[sokel] ✗ %s failed (%dms)%s: %s", op, _ms(started), tag, e)
                await publish_frame({"kind": "error", "text": str(e)})
            await publish_frame({"kind": "end"})
            return

        try:
            vars_ = await p.dispatch_buffered(call, files)
        except Exception as e:  # noqa: BLE001
            log.warning("[sokel] ✗ %s failed (%dms)%s: %s", op, _ms(started), tag, e)
            await nc.publish(msg.reply, json.dumps({"error": str(e)}).encode(), headers=headers)
            return
        log.info("[sokel] ✓ %s done (%dms)%s", op, _ms(started), tag)
        await nc.publish(msg.reply, json.dumps(vars_).encode(), headers=headers)


def _make_supervisor(p: Plugin, nc: Any, files: NatsFiles) -> SourceSupervisor:
    """One source instance per credential: its ctx is bound to that credential, and trigger carries
    the credential_id back automatically."""

    def start(cred: CredEntry):
        stopping = asyncio.Event()
        tasks: List[asyncio.Task] = []
        for src in p.sources:
            sctx = SourceCtx(
                token=p.token,
                publish=nc.publish,
                valid_events=p.contract.event_ids(),
                credential=cred.fields,
                credential_id=cred.id,
                source_id=src.id,
                board=p.board,
                files=files,
                stopping=stopping,
            )
            tasks.append(asyncio.create_task(_run_source(p, src, sctx, cred)))

        def stop() -> None:
            stopping.set()
            for t in tasks:
                t.cancel()
            p.board.remove_cred(cred.id)

        return stop

    return SourceSupervisor(start)


async def _run_source(p: Plugin, src: Any, sctx: SourceCtx, cred: CredEntry) -> None:
    log.info("[sokel] event source %r started (credential=%s)", src.id, cred.id or "(none)")
    p.board.set(src.id, cred.id, "running")
    try:
        await src.fn(sctx)
        p.board.set_if_running(src.id, cred.id, "exited")
    except asyncio.CancelledError:
        raise  # stopped by reconcile: stop() already removed the state wholesale
    except Exception as e:  # noqa: BLE001
        log.exception("[sokel] event source %r exited (credential=%s)", src.id, cred.id or "(none)")
        p.board.set(src.id, cred.id, "error", str(e))


def _resync(register: Any, supervisor: SourceSupervisor) -> None:
    async def go() -> None:
        try:
            _, _, creds = await register()
            supervisor.reconcile(desired_source_creds(creds))
        except Exception as e:  # noqa: BLE001
            log.warning("[sokel] re-register after a credential change failed: %s", e)

    asyncio.create_task(go())


class _Debouncer:
    """Collapse repeated triggers within a short window (a bulk credential edit re-registers once)."""

    def __init__(self, delay: float, fn: Any) -> None:
        self._delay = delay
        self._fn = fn
        self._handle: Optional[asyncio.TimerHandle] = None

    def trigger(self) -> None:
        loop = asyncio.get_running_loop()
        if self._handle is not None:
            self._handle.cancel()
        self._handle = loop.call_later(self._delay, self._fn)


async def _connect_forever(opts: Dict[str, Any]) -> Any:
    """Wait for a broker that is not up yet instead of exiting (Go's RetryOnFailedConnect)."""
    while True:
        try:
            return await nats.connect(**opts)
        except Exception as e:  # noqa: BLE001
            log.warning("[sokel] could not connect to the platform (%s), retrying in %ds…", e, RETRY_SEC)
            await asyncio.sleep(RETRY_SEC)


async def discover(endpoint: str, token: str) -> Dict[str, Any]:
    """The platform's /connect-info returns everything needed to reach the broker: its address,
    this access group's own credentials, and the broker's CA when it is outside the system trust
    store. Returns {"url", "user", "pass", "subject", "ca"} (all but url may be empty).

    A literal nats:// or tls:// URL skips discovery (local development, offline setups) -- no
    per-group credentials are available on that path.
    """
    ep = (endpoint or "").strip()
    if ep.startswith("nats://") or ep.startswith("tls://"):
        return {"url": ep}
    if not (ep.startswith("http://") or ep.startswith("https://")):
        raise ValueError(f"invalid endpoint {endpoint!r}: expected a platform URL (https://…) or nats://")
    url = ep.rstrip("/") + "/api/v1/connect-info"
    info = await asyncio.to_thread(_http_get_json, url, token)
    nats_obj = info.get("nats") or {}
    if nats_obj.get("url"):
        return nats_obj
    target = (info.get("transports") or {}).get("nats")
    if not target:
        raise RuntimeError("the platform offers no transport (connect-info.transports is empty)")
    return {"url": target}


def _http_get_json(url: str, token: str) -> Dict[str, Any]:
    import urllib.request

    req = urllib.request.Request(url, headers={"Authorization": f"Bearer {token}"})
    with urllib.request.urlopen(req, timeout=8) as resp:  # noqa: S310
        return json.loads(resp.read().decode())


def stable_instance_id(token: str) -> str:
    """A stable replica identity, reused across restarts. Without it every restart leaves the
    platform holding another ghost row that stays offline forever.

    In order: 1) SOKEL_INSTANCE_ID; 2) a file in the working directory named after the token's
    fingerprint; 3) if writing that file fails, host-pid.
    """
    if v := env.get("INSTANCE_ID"):
        return v
    import hashlib
    import secrets

    name = ".sokel-instance-id"
    if token:
        name += "." + hashlib.sha256(token.encode()).hexdigest()[:8]
    try:
        with open(name, "r", encoding="utf-8") as fh:
            if existing := fh.read().strip():
                return existing
    except OSError:
        pass
    host = socket.gethostname()
    ident = f"{host}-{secrets.token_hex(4)}"
    try:
        with open(name, "w", encoding="utf-8") as fh:
            fh.write(ident + "\n")
    except OSError:
        return f"{host}-{os.getpid()}"
    return ident


def _trace_tag(trace: Dict[str, str]) -> str:
    parts = [
        f"{label}={trace[key]}"
        for key, label in (("run_id", "run"), ("workflow_id", "wf"), ("node_id", "node"))
        if trace.get(key)
    ]
    return f" [{' '.join(parts)}]" if parts else ""


def _ms(started: float) -> int:
    return int((time.monotonic() - started) * 1000)


def _cb(fn: Any) -> Any:
    async def cb(*_args: Any, **_kw: Any) -> None:
        fn()

    return cb
