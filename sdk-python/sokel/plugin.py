"""Plugin：注册表 + 分发。传输无关——NATS 那一层只负责收发字节。

分发这段是**最值得单测的一段**（未知操作、流式与非流式的回复形态、webhook 帧的拦截），
所以它一行都不碰 NATS：测试塞一个假 sink 就能跑完整条路径。
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
    """一个插件实例。

    契约来自 sokel-gen 从 sokel.yaml 生成的 CONTRACT —— 声明与实现分离，
    这里只认「操作 id → 实现」。
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
        # 版本三档：显式参数 > 契约里声明的（sokel.yaml 的 plugin.version）> 环境变量 > 兜底。
        # 实例列表的「版本」列靠它回答「线上跑的是不是我刚发的那版」。
        self.version = version or self.contract.get("version") or env.get("VERSION") or "sdk-python"
        self._ops: Dict[str, Invoke] = {}
        self._sources: List[Source] = []
        self._webhook: Optional[Callable[[SourceCtx, WebhookRequest], Any]] = None
        self.board = StateBoard()
        self._managed = False

    # —— 注册 ——

    def register(self, op_id: str, fn: Invoke) -> None:
        """低阶注册：fn(ctx, input_dict, emitter)。生成的 on_xxx 就是它的类型化外壳。"""
        if op_id not in ("",) and self.contract.operation(op_id) is None and "." not in op_id:
            raise ValueError(
                f"操作 {op_id!r} 不在契约里 —— 先在 sokel.yaml 的 operations 声明它，再重新生成"
            )
        if op_id in self._ops:
            raise ValueError(f"操作 {op_id!r} 重复注册")
        self._ops[op_id] = fn

    def register_source(self, id: str, label: str, fn: Callable[[SourceCtx], Awaitable[None]]) -> None:
        """注册常驻事件源；run() 时每个「源 × 凭证」起一个 task。"""
        self._sources.append(Source(id, label, fn))

    def register_webhook(self, fn: Callable[[SourceCtx, WebhookRequest], Any]) -> None:
        """注册 webhook 处理器（一个插件一个：按 header / path 自行分流上游事件类型）。"""
        self._webhook = fn
        # 能力位不靠自报靠事实：注册了处理器就是支持，作者忘声明不该让入口按钮消失
        caps = dict(self.contract.get(C.KEY_CAPABILITIES) or {})
        caps[C.CAP_WEBHOOK] = True
        self.contract.set(C.KEY_CAPABILITIES, caps)

    def register_auth(
        self,
        start: Optional[Callable[[Ctx], Any]] = None,
        poll: Optional[Callable[[Ctx, str], Any]] = None,
        submit: Optional[Callable[[Ctx, str, str], Any]] = None,
    ) -> None:
        """挂上认证流的实现。形态（qr / input / oauth）在 sokel.yaml 里声明，这里只给实现。

        kind=oauth 的 start/poll 由**平台代答**（client_secret 在平台手里，插件构造不出
        同意页地址），所以那种插件一个 handler 都不用写。
        """
        declared = self.contract.get(C.KEY_AUTH_FLOW) or {}
        steps = list(declared.get("steps") or [])
        if start is not None:
            self._require_step(steps, "start")

            async def _start(ctx: Ctx, raw: Dict[str, Any], out: Emitter) -> None:
                ch = await _maybe_await(start(ctx))
                if ch is None:
                    raise RuntimeError("认证流 start 未返回挑战")
                if not isinstance(ch, AuthChallenge):
                    raise TypeError("认证流 start 必须返回 AuthChallenge")
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
                    raise TypeError("认证流 poll 必须返回 AuthState")
                vars_: Dict[str, Any] = {"status": st.status}
                # session 只在 confirmed 时带出去；pending 时带一个 null 会让平台反复覆写凭证行
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
                f"契约里的认证流没有 {step!r} 这一步（当前 {steps or '未声明'}）——"
                "步骤由 credential.auth.kind 定死，实现多于声明就是一份永远不会被调用的代码"
            )

    def set_capabilities(self, caps: Dict[str, bool]) -> None:
        """声明本插件支持/不支持哪些**可选**能力（同一个操作做到什么程度）。"""
        merged = dict(self.contract.get(C.KEY_CAPABILITIES) or {})
        merged.update(caps)
        self.contract.set(C.KEY_CAPABILITIES, merged)

    # —— 注册握手载荷 ——

    def register_payload(self, instance_id: str, host: str, started_at: str) -> Dict[str, Any]:
        """注册 / 心跳的载荷（协议 §3）。

        单独一个方法是为了能测：**声明了却没上报**是这套自报机制最典型的静默失效——
        插件侧一切正常、平台侧什么也没发生，作者只能对着一个没反应的界面排查。
        """
        from . import env

        body: Dict[str, Any] = {
            "token": self.token,
            "instance_id": instance_id,
            "host": host,
            # 进程启动时刻：注册与每拍心跳重发同一值，平台据此区分「重新上线的新副本」与「一直活着的老副本」
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

    # —— 分发 ——

    def find(self, op_id: str) -> Optional[Invoke]:
        fn = self._ops.get(op_id)
        if fn is not None:
            return fn
        # 单操作插件：operation 省略（或对不上）时默认唯一操作，与 Go SDK 同一条兜底
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
        """跑一次调用，把各帧交给 sink。异常原样抛出，由传输层翻成 error 帧 / error 回复。"""
        op_id = call.get("operation") or ""
        fn = self.find(op_id)
        if fn is None:
            raise LookupError(f"unknown operation {op_id!r}")
        ctx = Ctx(credential=call.get("credential"), trace=call.get("trace"), files=files)
        await fn(ctx, call.get("input") or {}, Emitter(sink))

    async def dispatch_buffered(
        self, call: Dict[str, Any], files: Optional[FileRuntime] = None
    ) -> Dict[str, Any]:
        """非流式：缓冲各帧，合并 variables 作为单次回复。"""
        sink = BufferSink()
        await self.dispatch(call, sink, files)
        return sink.vars

    async def handle_webhook(self, sctx: SourceCtx, frame: Dict[str, Any]) -> Dict[str, Any]:
        """处理一帧 __webhook__。应答带 events 计数——平台的 webhook 面板靠它回答
        「请求到了但为什么没起工作流」这一问。"""
        if self._webhook is None:
            return {"status": 0, "error": "插件未注册 webhook 处理器"}
        req = WebhookRequest.from_frame(frame or {})
        counted = _CountingCtx(sctx)
        try:
            resp = await _maybe_await(self._webhook(counted, req))
        except Exception as e:  # noqa: BLE001 —— 上游要的是一个明确的失败应答，不是插件进程退出
            log.exception("[sokel] webhook 处理失败")
            return {"status": 0, "error": f"{e}"}
        if resp is None:
            resp = WebhookResponse()
        if not isinstance(resp, WebhookResponse):
            return {"status": 0, "error": "webhook 处理器必须返回 WebhookResponse"}
        log.info("[sokel] ✓ webhook 处理完成（status=%s events=%d）", resp.status, counted.n)
        return resp.to_frame(counted.n)

    # —— 运行 ——

    async def run(self) -> None:
        """连接平台、注册、心跳、分发调用。阻塞到收到 SIGINT/SIGTERM。"""
        from .nats_transport import NatsTransport

        await NatsTransport().run(self)

    @property
    def sources(self) -> List[Source]:
        return self._sources

    @property
    def has_webhook(self) -> bool:
        return self._webhook is not None


class _CountingCtx:
    """数 trigger 成功次数（webhook 应答的 events 字段）。"""

    def __init__(self, inner: SourceCtx) -> None:
        self._inner = inner
        self.n = 0

    def __getattr__(self, item: str) -> Any:
        return getattr(self._inner, item)

    async def trigger(self, event: str, event_id: str, payload: Any) -> None:
        await self._inner.trigger(event, event_id, payload)
        self.n += 1


async def _maybe_await(v: Any) -> Any:
    """handler 可以是 async def，也可以是普通函数——两种都收。"""
    if inspect.isawaitable(v):
        return await v
    return v


def _new_auth_id() -> str:
    import time

    return f"auth_{int(time.time() * 1e9)}"
