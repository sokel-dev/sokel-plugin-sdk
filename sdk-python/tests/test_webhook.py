"""Webhooks received on the plugin's behalf: decoding the frame, replying, and counting events.

The event count is the platform webhook panel's only basis for answering "the request arrived, so why
did no workflow start", so it has to count **events successfully pushed**, not how many times the handler
ran.
"""

import base64
import json

from conftest import SIMPLE_CONTRACT

from sokel import Plugin, SourceCtx, WebhookRequest, ok, text


def make_frame(body: dict, headers: dict) -> dict:
    return {
        "method": "POST",
        "headers": headers,
        "body_b64": base64.b64encode(json.dumps(body).encode()).decode(),
    }


def make_sctx() -> SourceCtx:
    async def publish(subject: str, data: bytes) -> None:
        pass

    return SourceCtx(token="t", publish=publish, valid_events=["ping"], credential={"api_key": "secret"})


async def test_webhook_triggers_and_counts_events():
    p = Plugin(dict(SIMPLE_CONTRACT), name="demo", token="t")

    async def handler(ctx, req: WebhookRequest):
        assert req.header("x-sokel-token") == "secret"  # header lookup is case-insensitive
        await ctx.trigger("ping", "e1", {"at": "now"})
        return ok()

    p.register_webhook(handler)
    resp = await p.handle_webhook(make_sctx(), make_frame({"a": 1}, {"X-Sokel-Token": "secret"}))
    assert resp["status"] == 200
    assert resp["events"] == 1


async def test_webhook_can_reject_without_triggering():
    p = Plugin(dict(SIMPLE_CONTRACT), name="demo", token="t")
    p.register_webhook(lambda ctx, req: text(401, "bad token"))
    resp = await p.handle_webhook(make_sctx(), make_frame({}, {}))
    assert resp["status"] == 401
    assert base64.b64decode(resp["body_b64"]) == b"bad token"
    assert resp["events"] == 0


async def test_body_keeps_raw_bytes():
    """Signature checks are byte-exact: the body reaches the handler unchanged, never re-encoded as JSON."""
    p = Plugin(dict(SIMPLE_CONTRACT), name="demo", token="t")
    seen = {}

    def handler(ctx, req: WebhookRequest):
        seen["raw"] = req.body
        return ok()

    p.register_webhook(handler)
    raw = b'{"a":  1}'  # the extra space is deliberate: re-encoding would flatten it
    await p.handle_webhook(make_sctx(), {"body_b64": base64.b64encode(raw).decode()})
    assert seen["raw"] == raw


async def test_no_handler_reports_a_readable_failure():
    p = Plugin(dict(SIMPLE_CONTRACT), name="demo", token="t")
    resp = await p.handle_webhook(make_sctx(), make_frame({}, {}))
    assert resp["status"] == 0 and "no webhook handler" in resp["error"]
