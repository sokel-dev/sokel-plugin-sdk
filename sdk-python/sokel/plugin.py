# Copyright 2026 The Sokel Authors
# SPDX-License-Identifier: Apache-2.0

"""Plugin: the registry plus dispatch. Transport-agnostic — the NATS layer only moves bytes.

Dispatch is the part **most worth unit-testing** (unknown operations, the reply shape for streaming
versus non-streaming, intercepting the webhook frame), so it touches no NATS at all: a fake sink is
enough to exercise the whole path.
"""

from __future__ import annotations

import inspect
import logging
import traceback
from typing import Any, Awaitable, Callable, Dict, List, Optional

from . import contract as C
from .auth import AuthChallenge, AuthState
from .contract import Contract
from .events import Source, SourceCtx, StateBoard
from .runtime import BufferSink, Ctx, Emitter, FileRuntime
from .webhook import WebhookRequest, WebhookResponse

log = logging.getLogger("sokel")

Invoke = Callable[[Ctx, Dict[str, Any], Emitter], Awaitable[None]]


class Plugin:
    """One plugin instance.

    The contract is the CONTRACT that sokel-gen renders from sokel.yaml: declaration and
    implementation stay apart, and all this class knows is "operation id -> implementation".
    """

    def __init__(
        self,
        contract: Optional[Dict[str, Any]] = None,
        name: str = "",
        endpoint: str = "",
        token: str = "",
        version: str = "",
    ) -> None:
        from . import env

        self.contract = Contract(contract)
        self.name = name or self.contract.get("name") or "sokel-plugin"
        self.endpoint = endpoint or env.get_or("ENDPOINT", "http://localhost:8088")
        self.token = token or env.get("TOKEN")
        # Version, in order of precedence: explicit argument > the contract's plugin.version >
        # environment variable > fallback. The replica list's "version" column is what answers
        # "is what's running out there the build I just shipped?".
        self.version = version or self.contract.get("version") or env.get("VERSION") or "sdk-python"
        self._ops: Dict[str, Invoke] = {}
        self._sources: List[Source] = []
        self._webhook: Optional[Callable[[SourceCtx, WebhookRequest], Any]] = None
        self.board = StateBoard()
        self._managed = False

    # —— registration ——

    def register(self, op_id: str, fn: Invoke) -> None:
        """Low-level registration: fn(ctx, input_dict, emitter). The generated on_xxx is its typed shell."""
        if op_id not in ("",) and self.contract.operation(op_id) is None and "." not in op_id:
            raise ValueError(
                f"operation {op_id!r} is not in the contract — declare it under operations in "
                f"sokel.yaml and regenerate"
            )
        if op_id in self._ops:
            raise ValueError(f"operation {op_id!r} registered twice")
        self._ops[op_id] = fn

    def register_source(self, id: str, label: str, fn: Callable[[SourceCtx], Awaitable[None]]) -> None:
        """Register a long-running event source; run() starts one task per source × credential."""
        self._sources.append(Source(id, label, fn))

    def register_webhook(self, fn: Callable[[SourceCtx, WebhookRequest], Any]) -> None:
        """Register the webhook handler (one per plugin: route upstream event types yourself by
        header or path)."""
        self._webhook = fn
        # The capability follows the fact, not a declaration: registering a handler *is* support.
        # Forgetting to declare it should never make the entry-point button disappear.
        caps = dict(self.contract.get(C.KEY_CAPABILITIES) or {})
        caps[C.CAP_WEBHOOK] = True
        self.contract.set(C.KEY_CAPABILITIES, caps)

    def register_auth(
        self,
        start: Optional[Callable[[Ctx], Any]] = None,
        poll: Optional[Callable[[Ctx, str], Any]] = None,
        submit: Optional[Callable[[Ctx, str, str], Any]] = None,
    ) -> None:
        """Attach the auth flow's implementation. The shape (qr / input / oauth) is declared in
        sokel.yaml; only the implementation goes here.

        For kind=oauth the platform answers start/poll itself — the client secret lives there and a
        plugin cannot build the consent URL — so such a plugin writes no handler at all.
        """
        declared = self.contract.get(C.KEY_AUTH_FLOW) or {}
        steps = list(declared.get("steps") or [])
        if start is not None:
            self._require_step(steps, "start")

            async def _start(ctx: Ctx, raw: Dict[str, Any], out: Emitter) -> None:
                ch = await _maybe_await(start(ctx))
                if ch is None:
                    raise RuntimeError("the auth flow's start returned no challenge")
                if not isinstance(ch, AuthChallenge):
                    raise TypeError("the auth flow's start must return an AuthChallenge")
                auth_id = ch.auth_id or _new_auth_id()
                out.vars(
                    {
                        "auth_id": auth_id,
                        "challenge": {
                            "kind": ch.kind or declared.get("kind", ""),
                            "qr_image": ch.qr_image,
                            "prompt": ch.prompt,
                        },
                        "expires_in": ch.expires_in,
                    }
                )

            self._ops[C.OP_AUTH_START] = _start
        if poll is not None:
            self._require_step(steps, "poll")

            async def _poll(ctx: Ctx, raw: Dict[str, Any], out: Emitter) -> None:
                st = await _maybe_await(poll(ctx, raw.get("auth_id") or ""))
                if st is None:
                    st = AuthState()
                if not isinstance(st, AuthState):
                    raise TypeError("the auth flow's poll must return an AuthState")
                vars_: Dict[str, Any] = {"status": st.status}
                # Only carry the session once confirmed: handing back a null while pending makes the
                # platform rewrite the credential row over and over.
                if st.status == "confirmed" and st.session:
                    vars_["session"] = st.session
                out.vars(vars_)

            self._ops[C.OP_AUTH_POLL] = _poll
        if submit is not None:
            self._require_step(steps, "submit")

            async def _submit(ctx: Ctx, raw: Dict[str, Any], out: Emitter) -> None:
                await _maybe_await(submit(ctx, raw.get("auth_id") or "", raw.get("input") or ""))
                out.vars({"ok": True})

            self._ops[C.OP_AUTH_SUBMIT] = _submit

    def _require_step(self, steps: List[str], step: str) -> None:
        if step not in steps:
            raise ValueError(
                f"the contract's auth flow has no {step!r} step (it has {steps or 'none'}) — "
                f"the steps follow from credential.auth.kind, and implementing more than was declared "
                f"means writing code that will never be called"
            )

    def set_capabilities(self, caps: Dict[str, bool]) -> None:
        """Declare which **optional** capabilities this plugin has — how far a given operation goes."""
        merged = dict(self.contract.get(C.KEY_CAPABILITIES) or {})
        merged.update(caps)
        self.contract.set(C.KEY_CAPABILITIES, merged)

    # —— registration payload ——

    def register_payload(self, instance_id: str, host: str, started_at: str) -> Dict[str, Any]:
        """The registration / heartbeat payload (protocol §3).

        It is a separate method so it can be tested: **declared but never reported** is the classic
        silent failure of a self-reporting mechanism. Everything looks fine on the plugin side,
        nothing happens on the platform side, and the author is left staring at an inert UI.
        """
        from . import env

        body: Dict[str, Any] = {
            "token": self.token,
            "instance_id": instance_id,
            "host": host,
            # Process start time: registration and every heartbeat resend the same value, which is
            # how the platform tells "a new replica came up" from "the old one is still alive".
            "started_at": started_at,
            "version": self.version,
            "transport": "nats",
            "managed": self._managed,
        }
        if region := env.get("REGION"):
            body["region"] = region
        body.update(self.contract.payload())
        if self._sources:
            body["source_states"] = self.board.snapshot()
        return body

    # —— dispatch ——

    def find(self, op_id: str) -> Optional[Invoke]:
        fn = self._ops.get(op_id)
        if fn is not None:
            return fn
        # Single-operation plugin: when `operation` is missing (or unknown), fall back to the only
        # one there is — the same fallback the Go SDK has.
        business = [k for k in self._ops if "." not in k]
        if len(business) == 1:
            return self._ops[business[0]]
        return None

    async def dispatch(
        self,
        call: Dict[str, Any],
        sink: Callable[[Dict[str, Any]], None],
        files: Optional[FileRuntime] = None,
    ) -> None:
        """Run one call, handing frames to the sink. Exceptions propagate; the transport turns them
        into an error frame or an error reply."""
        op_id = call.get("operation") or ""
        fn = self.find(op_id)
        if fn is None:
            raise LookupError(f"unknown operation {op_id!r}")
        ctx = Ctx(credential=call.get("credential"), trace=call.get("trace"), files=files)
        await fn(ctx, call.get("input") or {}, Emitter(sink))

    async def dispatch_buffered(
        self, call: Dict[str, Any], files: Optional[FileRuntime] = None
    ) -> Dict[str, Any]:
        """Non-streaming: buffer the frames and merge the variables into a single reply."""
        sink = BufferSink()
        await self.dispatch(call, sink, files)
        return sink.vars

    async def handle_webhook(self, sctx: SourceCtx, frame: Dict[str, Any]) -> Dict[str, Any]:
        """Handle one __webhook__ frame. The reply carries an events count: that is how the
        platform's webhook panel answers "the request arrived, so why did no workflow start?"."""
        if self._webhook is None:
            return {"status": 0, "error": "the plugin registered no webhook handler"}
        req = WebhookRequest.from_frame(frame or {})
        counted = _CountingCtx(sctx)
        try:
            resp = await _maybe_await(self._webhook(counted, req))
        except Exception as e:  # noqa: BLE001 — upstream wants a definite failure reply, not a dead plugin
            log.exception("[sokel] webhook handling failed")
            return {"status": 0, "error": f"{e}"}
        if resp is None:
            resp = WebhookResponse()
        if not isinstance(resp, WebhookResponse):
            return {"status": 0, "error": "the webhook handler must return a WebhookResponse"}
        log.info("[sokel] ✓ webhook handled (status=%s events=%d)", resp.status, counted.n)
        return resp.to_frame(counted.n)

    # —— running ——

    async def run(self) -> None:
        """Connect, register, heartbeat and dispatch calls. Blocks until SIGINT/SIGTERM."""
        from .nats_transport import NatsTransport

        await NatsTransport().run(self)

    @property
    def sources(self) -> List[Source]:
        return self._sources

    @property
    def has_webhook(self) -> bool:
        return self._webhook is not None


class _CountingCtx:
    """Counts successful triggers (the events field of the webhook reply)."""

    def __init__(self, inner: SourceCtx) -> None:
        self._inner = inner
        self.n = 0

    def __getattr__(self, item: str) -> Any:
        return getattr(self._inner, item)

    async def trigger(self, event: str, event_id: str, payload: Any) -> None:
        await self._inner.trigger(event, event_id, payload)
        self.n += 1


async def _maybe_await(v: Any) -> Any:
    """A handler may be an `async def` or a plain function; both are accepted."""
    if inspect.isawaitable(v):
        return await v
    return v


def _new_auth_id() -> str:
    import time

    return f"auth_{int(time.time() * 1e9)}"
