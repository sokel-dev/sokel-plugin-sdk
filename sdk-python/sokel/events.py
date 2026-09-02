# Copyright 2026 The Sokel Authors
# SPDX-License-Identifier: Apache-2.0

"""Event sources: the plugin pushes external events to the platform to start workflows (protocol §7).

How this differs from operations: an operation is request/reply (the platform calls the plugin); an
event is fire-and-forget (the plugin pushes to the platform).

Many bots, one replica (protocol v1.3): every registration and heartbeat returns "the subset of
credentials assigned to this replica", and the supervisor reconciles against it — one source instance
per credential, cancelled when the credential goes away, restarted when its fields change.
"""

from __future__ import annotations

import asyncio
import json
import logging
from typing import Any, Awaitable, Callable, Dict, List, Optional

from pydantic import BaseModel

from .runtime import Ctx, File, FileRuntime, _to_vars

log = logging.getLogger("sokel")

TRIGGER_SUBJECT = "sokel.trigger"
CREDENTIAL_UPDATE_SUBJECT = "sokel.credential.update"


class CredEntry:
    """One entry of the registration reply's credentials list — a bot identity assigned here."""

    def __init__(self, id: str = "", fields: Optional[Dict[str, str]] = None) -> None:
        self.id = id
        self.fields = dict(fields or {})

    def sig(self) -> str:
        """A stable signature of the fields; reconcile uses it to decide "fields changed -> restart"."""
        return "\n".join(f"{k}={self.fields[k]}" for k in sorted(self.fields))


class StateBoard:
    """Runtime state per source × credential. Reported with each registration/heartbeat so the panel
    can show every bot."""

    def __init__(self, now: Optional[Callable[[], str]] = None) -> None:
        self._m: Dict[str, Dict[str, Any]] = {}
        self._now = now or _rfc3339_now

    def set(self, source_id: str, cred_id: str, status: str, error: str = "") -> None:
        entry = {"source_id": source_id, "status": status, "since": self._now()}
        if cred_id:
            entry["credential_id"] = cred_id
        if error:
            entry["error"] = error
        self._m[f"{source_id}|{cred_id}"] = entry

    def set_if_running(self, source_id: str, cred_id: str, status: str, error: str = "") -> None:
        """Overwrite only while the instance is still `running`.

        A source that reported its own status (auth_required, say) and then returned normally should
        not have that sentence overwritten on the way out: all the panel would show is "exited", and
        *why* it exited is the half that matters.
        """
        cur = self._m.get(f"{source_id}|{cred_id}")
        if cur is None or cur.get("status") == "running":
            self.set(source_id, cred_id, status, error)

    def remove_cred(self, cred_id: str) -> None:
        for k in [k for k, v in self._m.items() if v.get("credential_id", "") == cred_id]:
            del self._m[k]

    def snapshot(self) -> List[Dict[str, Any]]:
        return sorted(self._m.values(), key=lambda s: (s["source_id"], s.get("credential_id", "")))


def _rfc3339_now() -> str:
    import datetime

    return datetime.datetime.now(datetime.timezone.utc).astimezone().isoformat(timespec="seconds")


class SourceCtx:
    """The context for a long-running source or a webhook: push events, read and write back
    credentials, upload attachments, report state."""

    def __init__(
        self,
        token: str,
        publish: Callable[[str, bytes], Awaitable[None]],
        valid_events: Optional[List[str]] = None,
        credential: Optional[Dict[str, str]] = None,
        credential_id: str = "",
        source_id: str = "",
        board: Optional[StateBoard] = None,
        files: Optional[FileRuntime] = None,
        stopping: Optional[asyncio.Event] = None,
    ) -> None:
        self._token = token
        self._publish = publish
        self._valid = set(valid_events or [])
        self.credential: Dict[str, str] = dict(credential or {})
        self.credential_id = credential_id
        self.source_id = source_id
        self._board = board
        self._files = files
        # stopping is set when reconcile stops this instance. Long-poll loops run
        # `while not ctx.stopping.is_set()`.
        self.stopping = stopping or asyncio.Event()

    def credential_as(self, model: type) -> Any:
        return model(**{k: v for k, v in self.credential.items() if v != ""})

    async def trigger(self, event: str, event_id: str, payload: Any) -> None:
        """Push one event (fire-and-forget).

        `event` must be a declared event id: a typo fails here rather than turning into a message
        nobody on the platform claims. That failure mode has no symptoms — the plugin log looks fine
        and the workflow simply never starts. `event_id` is the idempotency key; the platform
        deduplicates on (plugin, event, event_id).
        """
        if self._valid and event not in self._valid:
            raise ValueError(f"undeclared event {event!r} — declare it under events in manifest.yml")
        msg: Dict[str, Any] = {"token": self._token, "event": event, "payload": _to_vars(payload)}
        if event_id:
            msg["event_id"] = event_id
        if self.credential_id:
            msg["credential_id"] = self.credential_id
        await self._publish(TRIGGER_SUBJECT, json.dumps(msg).encode())

    async def update_credential(self, patch: Dict[str, str]) -> None:
        """Write a patch back to the credential bound to this instance (how a session-style credential
        refreshes itself while running).

        The platform is the only store for credentials; a plugin never persists them locally.
        """
        if not self.credential_id:
            raise RuntimeError("this source instance has no bound credential to write back to")
        if not patch:
            return
        self.credential.update(patch)
        data = json.dumps(
            {"token": self._token, "credential_id": self.credential_id, "patch": patch}
        ).encode()
        await self._publish(CREDENTIAL_UPDATE_SUBJECT, data)

    def report_status(self, status: str, msg: str = "") -> None:
        """Report state (an expired session becomes auth_required); it rides the heartbeat and lights
        up "needs login" in the panel."""
        if self._board is not None:
            self._board.set(self.source_id, self.credential_id, status, msg)

    async def upload(self, name: str, mime: str, data: bytes) -> File:
        if self._files is None:
            return File(name=name, mime=mime, size=len(data), data=data)
        return await self._files.store(name, mime, data)

    async def upload_file(self, path: str, name: str = "", mime: str = "") -> File:
        """Stream a local file (same semantics as Ctx.upload_file on the operation side)."""
        return await Ctx(files=self._files).upload_file(path, name, mime)

    async def fetch(self, f: File) -> bytes:
        if f.data is not None:
            return f.data
        if self._files is None:
            raise RuntimeError("file runtime not ready")
        return await self._files.fetch(f)


class Source:
    """A long-running event source. The SDK runs fn in its own task; inside, ctx.trigger pushes."""

    def __init__(self, id: str, label: str, fn: Callable[[SourceCtx], Awaitable[None]]) -> None:
        self.id = id
        self.label = label
        self.fn = fn


class SourceSupervisor:
    """Per-credential supervisor: starts, stops and restarts instances to match the assigned set."""

    def __init__(self, start: Callable[[CredEntry], Callable[[], None]]) -> None:
        self._start = start
        self._running: Dict[str, Any] = {}  # cred_id -> (stop, sig)

    def reconcile(self, desired: List[CredEntry]) -> None:
        want = {c.id: c for c in desired}
        # Stop: not in the desired set, or its fields changed (stop then start = restart)
        for cid in list(self._running):
            stop, sig = self._running[cid]
            c = want.get(cid)
            if c is not None and c.sig() == sig:
                continue
            stop()
            del self._running[cid]
        # Start: desired but not running
        for cid, c in want.items():
            if cid in self._running:
                continue
            self._running[cid] = (self._start(c), c.sig())

    def stop_all(self) -> None:
        for stop, _ in self._running.values():
            stop()
        self._running.clear()


def desired_source_creds(creds: List[CredEntry]) -> List[CredEntry]:
    """Empty (a plugin with no credentials) becomes one bare instance, so both cases take the same
    code path."""
    return creds or [CredEntry()]
