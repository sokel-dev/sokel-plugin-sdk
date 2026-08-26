"""全能示例插件（Python 实现）。

契约在 ../sokel.yaml 里声明，sokel_gen.py 由它生成 —— 本文件只写实现，全程 typed。
Node 版实现的是**同一份声明**（../node/src/main.ts），两边上报的契约必须逐字节相同。

运行：

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


# —— 操作：每种字段形态原样回显 ——


async def echo_all(ctx: Ctx, in_: EchoAllIn) -> EchoAllOut:
    cred = credential(ctx)  # 类型化读凭证：裸 ctx.credential["api_key"] 拼错是静默绑空
    # 结构联合：运行值就是分支本身的形状（不带 discriminator），按类型判别即可
    if isinstance(in_.doc, DocObject):
        doc_desc = f"文档对象《{in_.doc.title}》"
    elif isinstance(in_.doc, list):
        doc_desc = f"块数组（{len(in_.doc)} 块）"
    else:
        doc_desc = "未给文档"
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


# —— 操作：文件入参惰性取字节，出参把字节交回平台登记 ——


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


# —— 操作：流式 ——


async def chat_stream(ctx: Ctx, in_: ChatStreamIn, out: Emitter) -> None:
    frames = max(in_.chunks, 1)
    reply = ""
    for i in range(frames):
        piece = f"{in_.prompt}#{i + 1} "
        reply += piece
        out.text(piece)  # 人类可读增量：节点执行时实时可见
        await asyncio.sleep(0.05)
    # 末尾产出类型化变量：进下游节点、按 Outputs 契约校验
    out.vars(ChatStreamOut(reply=reply.strip(), frames=frames))


on_chat_stream(p, chat_stream)


# —— 凭证体检：平台约定的保留 id，凭证页「测试」调的就是它 ——


def health_check(ctx: Ctx, _in: HealthCheckIn) -> HealthCheckOut:
    cred = credential(ctx)
    # 不可用要**返回 ok=false 而不是抛错**：抛错的话平台只能说「调用失败」，
    # 说不出是密钥没配还是上游拒绝，而这两件事的处理办法完全不同。
    if not cred.api_key:
        return HealthCheckOut(ok=False, message="凭证里没有 API Key")
    if not cred.api_key.startswith("sk-"):
        return HealthCheckOut(ok=False, message=f"API Key 形如 sk-…，当前是「{cred.api_key[:6]}…」")
    # 真插件在这里发一个最廉价的上游请求（如 GET /me），把上游原文带回 message
    return HealthCheckOut(ok=True, message=f"{cred.base_url} / {cred.region} 可用")


on_health_check(p, health_check)


# —— webhook：验签 → 推 typed 事件 ——


def handle_webhook(ctx: SourceCtx, req: WebhookRequest) -> WebhookResponse:
    cred = credential(ctx)   # 事件源 ctx 与操作 ctx 都能这么读
    # 验上游签名是**插件的职责**：各家算法不同，平台不懂上游、插件懂
    if req.header("X-Sokel-Token") != cred.api_key:
        return text(401, "bad token")
    body = json.loads(req.body or b"{}")
    asyncio.get_running_loop().create_task(
        trigger_message(
            ctx,
            str(body.get("id") or ""),  # 幂等键：上游重发只触发一次
            MessageEvent(chat_id=str(body.get("chat_id") or ""), text=str(body.get("text") or "")),
        )
    )
    return ok()


p.register_webhook(handle_webhook)


# —— 常驻事件源：每 30 秒推一条心跳 ——


async def heartbeat(ctx: SourceCtx) -> None:
    cred = credential(ctx)
    if not cred.api_key:
        # 没凭证就自报「待登录」，面板上这一行会亮起来，而不是静悄悄地什么都不做
        ctx.report_status("auth_required", "凭证还没登录")
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


p.register_source("heartbeat", "心跳", heartbeat)


# —— 协作式认证（kind=input：start → poll → submit）——

_pending: Dict[str, str] = {}  # auth_id → 已提交的验证码；生产里换成带过期的存储


def auth_start(ctx: Ctx) -> AuthChallenge:
    auth_id = f"demo-{int(time.time())}"
    _pending[auth_id] = ""
    return AuthChallenge(auth_id=auth_id, prompt="请输入任意 6 位数字作为验证码", expires_in=300)


def auth_submit(ctx: Ctx, auth_id: str, user_input: str) -> None:
    if len(user_input) != 6 or not user_input.isdigit():
        raise ValueError("验证码应为 6 位数字")
    _pending[auth_id] = user_input


def auth_poll(ctx: Ctx, auth_id: str) -> AuthState:
    code = _pending.get(auth_id)
    if not code:
        return AuthState(status="pending")
    # 只有 confirmed 才带 session：中途带出去等于让平台反复覆写凭证行
    return AuthState(status="confirmed", session={"api_key": f"sk-demo-{code}"})


p.register_auth(start=auth_start, poll=auth_poll, submit=auth_submit)


if __name__ == "__main__":
    asyncio.run(p.run())
