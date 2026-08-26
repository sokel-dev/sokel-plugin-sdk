# Copyright 2026 The Sokel Authors
# SPDX-License-Identifier: Apache-2.0

"""Runtime shapes: file references, output frames, the call context.

Transport-agnostic — NATS supplies only three callbacks (fetch bytes, store bytes, emit a frame);
every other semantic lives here. That is why tests need no broker: a fake file runtime and a fake
sink are enough to exercise the whole dispatch path.
"""

from __future__ import annotations

from typing import Any, Awaitable, Callable, Dict, List, Optional, Protocol

from pydantic import BaseModel, Field as _PField


class File(BaseModel):
    """A file reference. Only the reference travels through the canvas; bytes never inline."""

    id: str = ""
    url: str = ""
    name: str = ""
    mime: str = ""
    size: int = 0
    # data: bytes carried directly when there is no platform file layer (unit tests). Not serialized.
    data: Optional[bytes] = _PField(default=None, exclude=True)

    async def blob(self, ctx: "Ctx") -> bytes:
        """Fetch the bytes lazily, chunk by chunk through the platform's file layer."""
        if self.data is not None:
            return self.data
        return await ctx.fetch(self)


class FileRuntime(Protocol):
    """The fetch/store backend for file bytes, injected by the transport."""

    async def fetch(self, f: File) -> bytes: ...

    async def store(self, name: str, mime: str, data: bytes) -> File: ...

    async def store_stream(self, name: str, mime: str, src: Any) -> File: ...


# —— output frames (protocol §4's streaming frames) ——

FRAME_TEXT = "text"
FRAME_JSON = "json"
FRAME_VARS = "variables"


def _to_vars(value: Any) -> Dict[str, Any]:
    """Output object -> a dict keyed by contract names (a pydantic model's field names are them)."""
    if value is None:
        return {}
    if isinstance(value, BaseModel):
        return value.model_dump(mode="json", exclude_none=True)
    if isinstance(value, dict):
        return dict(value)
    raise TypeError(f"output must be a pydantic model or a dict, got {type(value).__name__}")


class Emitter:
    """Typed emitter. Each call is one frame (streaming); for non-streaming operations the SDK buffers
    the frames and merges them into a single reply."""

    def __init__(self, sink: Callable[[Dict[str, Any]], None]) -> None:
        self._sink = sink

    def text(self, s: str) -> None:
        """Human-readable text (display / tracing)."""
        self._sink({"kind": FRAME_TEXT, "text": s})

    def json(self, v: Any) -> None:
        """Structured JSON (display / tracing)."""
        self._sink({"kind": FRAME_JSON, "json": v})

    def vars(self, out: Any) -> None:
        """Typed output variables (they flow to downstream nodes). May be called repeatedly; a later
        frame overwrites same-named fields."""
        m = _to_vars(out)
        if m:
            self._sink({"kind": FRAME_VARS, "vars": m})


class BufferSink:
    """Non-streaming sink: keeps only variables frames and merges them into one output object.
    text/json frames exist for streaming display only."""

    def __init__(self) -> None:
        self.vars: Dict[str, Any] = {}

    def __call__(self, frame: Dict[str, Any]) -> None:
        if frame.get("kind") != FRAME_VARS:
            return
        self.vars.update(frame.get("vars") or {})


class Ctx:
    """The context handed to an operation handler: credentials, tracing, file fetch/store."""

    def __init__(
        self,
        credential: Optional[Dict[str, str]] = None,
        trace: Optional[Dict[str, str]] = None,
        files: Optional[FileRuntime] = None,
    ) -> None:
        self.credential: Dict[str, str] = dict(credential or {})
        self._trace: Dict[str, str] = dict(trace or {})
        self._files = files

    def trace(self, key: str) -> str:
        """Tracing context supplied by the platform (run_id / workflow_id / node_id).

        Calls outside a workflow (console tests, health checks) have none of these and get "" back.
        **Treat "" as "no retry semantics"**, never as a constant key — doing the latter would
        deduplicate two independent calls into one.
        """
        return self._trace.get(key, "")

    def credential_as(self, model: type) -> Any:
        """Read the credential into a typed model. Missing keys fall back to the model's defaults;
        credential values are always strings."""
        return model(**{k: v for k, v in self.credential.items() if v != ""})

    async def fetch(self, f: File) -> bytes:
        if f.data is not None:
            return f.data
        if self._files is None:
            raise RuntimeError("file runtime not ready")
        return await self._files.fetch(f)

    async def upload(self, name: str, mime: str, data: bytes) -> File:
        """Produce a file: hand the bytes back to the platform and get a reference for the output."""
        if self._files is None:
            return File(name=name, mime=mime, size=len(data), data=data)
        return await self._files.store(name, mime, data)

    async def upload_file(self, path: str, name: str = "", mime: str = "") -> File:
        """Stream a local file: memory stays at one chunk (1 MiB) regardless of file size.

        Anything above a few hundred megabytes (video, archives, datasets) belongs here. upload()
        reads the whole file into memory first, and the symptom of that is a container mysteriously
        killed by the OOM reaper on large inputs.
        """
        import mimetypes
        import os

        name = name or os.path.basename(path)
        mime = mime or (mimetypes.guess_type(name)[0] or "application/octet-stream")
        if self._files is None:  # bare ctx (tests): read it in and hand the bytes back directly
            with open(path, "rb") as fh:
                data = fh.read()
            return File(name=name, mime=mime, size=len(data), data=data)
        with open(path, "rb") as fh:
            return await self._files.store_stream(name, mime, fh)


Handler = Callable[..., Any]
AsyncHandler = Callable[..., Awaitable[Any]]
