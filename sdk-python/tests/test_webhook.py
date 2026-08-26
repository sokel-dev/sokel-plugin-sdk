"""平台代收 webhook：帧的解与应答，以及 events 计数。

events 计数是平台 webhook 面板回答「请求到了但为什么没起工作流」的唯一依据，
所以它必须数的是**成功推出去的**事件，而不是 handler 被调了几次。
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
        assert req.header("x-sokel-token") == "secret"  # 取头大小写不敏感
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
    """验签要逐字节一致：body 必须原样送到 handler，不能被 JSON 重编码。"""
    p = Plugin(dict(SIMPLE_CONTRACT), name="demo", token="t")
    seen = {}

    def handler(ctx, req: WebhookRequest):
        seen["raw"] = req.body
        return ok()

    p.register_webhook(handler)
    raw = b'{"a":  1}'  # 故意留下多余空格：重编码会把它抹平
    await p.handle_webhook(make_sctx(), {"body_b64": base64.b64encode(raw).decode()})
    assert seen["raw"] == raw


async def test_no_handler_reports_a_readable_failure():
    p = Plugin(dict(SIMPLE_CONTRACT), name="demo", token="t")
    resp = await p.handle_webhook(make_sctx(), make_frame({}, {}))
    assert resp["status"] == 0 and "未注册" in resp["error"]
