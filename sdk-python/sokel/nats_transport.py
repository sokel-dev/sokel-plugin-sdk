"""NATS 承载：插件**出站**连 broker（无入站端口、无公网 IP、无防火墙洞）。

流程与 Go SDK 逐条对齐（协议 §1-§7）：发现 → 连接 → 注册（上报契约）→ 订阅（队列组）→
心跳续约 → 分发调用 → 优雅下线。事件源插件另有 per-credential supervisor。
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

QUEUE_GROUP = "sokel-workers"  # 同组多副本共享队列组 → 每次调用只投一个副本（真·负载均衡）
INSTANCE_HEADER = "Sokel-Instance"  # 回包/流帧自报副本身份
FILE_CHUNK = 1 << 20  # 1 MiB：字节不走操作 reply（受 max_payload 约束），走专用分块通道
HEARTBEAT_SEC = 20
RETRY_SEC = 8
REQUEST_TIMEOUT = 8
FILE_TIMEOUT = 30


class NatsFiles:
    """经同一条 NATS 连接与平台交换文件字节。不要求插件可达平台 HTTP —— 内网插件同样可用。"""

    def __init__(self, nc: Any, token: str) -> None:
        self._nc = nc
        self._token = token

    async def fetch(self, f: File) -> bytes:
        fid = f.id or (f.url.rsplit("/", 1)[-1] if f.url else "")
        if not fid:
            raise ValueError("文件引用缺少 id/url")
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
        """整块字节。走 store_stream —— 分块协议只该有一份实现。"""
        return await self.store_stream(name, mime, io.BytesIO(data))

    async def store_stream(self, name: str, mime: str, src: Any) -> File:
        """**边读边传**，内存占用恒为一个块。

        平台那侧本来就是逐块写进 blob 的，瓶颈一直只在插件这边。
        """
        upload_id = ""
        seq = 0
        while True:
            chunk = src.read(FILE_CHUNK)
            # 读不满即到底；空文件也要走一轮（last=True, 0 字节），否则平台侧没有会话可收尾
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
                    raise RuntimeError("平台未返回文件引用")
                return File(**f)
            seq += 1


class NatsTransport:
    async def run(self, p: Plugin) -> None:
        target = await discover(p.endpoint, p.token)
        # broker 的传输层鉴权优先用 SOKEL_NATS_TOKEN；缺省回退接入 token（无鉴权 broker 会忽略它）
        nats_token = env.get("NATS_TOKEN") or p.token
        opts: Dict[str, Any] = {
            "servers": [target],
            "name": p.name,
            "max_reconnect_attempts": -1,  # 无限重连；订阅在重连后自动恢复
            "reconnect_time_wait": 2,
            "disconnected_cb": _cb(lambda: log.warning("[sokel] 与平台断开（自动重连中）")),
            "reconnected_cb": _cb(lambda: log.info("[sokel] 已重连平台")),
        }
        if nats_token:
            opts["token"] = nats_token
        if ca := env.get("NATS_CA"):  # 连 TLS broker 且证书非系统信任时的自定义 CA
            import ssl

            ctx = ssl.create_default_context(cafile=ca)
            opts["tls"] = ctx
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
                raise RuntimeError(f"注册被拒：{reg.get('error') or '平台未给 subject'}")
            if reg.get("notify_subject"):
                state["notify"] = reg["notify_subject"]
            creds = [CredEntry(c.get("id", ""), c.get("fields")) for c in (reg.get("credentials") or [])]
            # 旧平台只有单数形态 → 折算成单元素集合（行为与旧版一致）
            if not creds and (reg.get("credential_id") or reg.get("credential")):
                creds = [CredEntry(reg.get("credential_id", ""), reg.get("credential"))]
            return reg["subject"], reg.get("name") or p.name, creds

        # 启动注册失败不退出（broker / 平台可能尚未就绪），固定间隔重试直到成功
        while True:
            try:
                subject, name, creds = await register()
                break
            except Exception as e:  # noqa: BLE001
                log.warning("[sokel] 注册失败（%s），%ds 后重试…", e, RETRY_SEC)
                await asyncio.sleep(RETRY_SEC)

        async def on_call(msg: Msg) -> None:
            await self._dispatch(p, nc, msg, files, instance_id)

        await nc.subscribe(subject, queue=QUEUE_GROUP, cb=on_call)
        log.info("[sokel] 已接入平台：插件「%s」就绪，副本 %s 监听 %s", name, instance_id, subject)

        supervisor: Optional[SourceSupervisor] = None
        if p.sources:
            supervisor = _make_supervisor(p, nc, files)
            supervisor.reconcile(desired_source_creds(creds))
            if state["notify"]:
                # 凭证变更即时通知：普通订阅（非队列组），组内每个副本都要收——各自的分配集合都可能变
                debounce = _Debouncer(0.3, lambda: _resync(register, supervisor))

                async def on_notify(_msg: Msg) -> None:
                    debounce.trigger()

                # 回调必须是协程：nats-py 对普通函数直接抛「must use coroutine for subscriptions」
                await nc.subscribe(state["notify"], cb=on_notify)

        stop = asyncio.Event()
        loop = asyncio.get_running_loop()
        for sig in (signal.SIGINT, signal.SIGTERM):
            try:
                loop.add_signal_handler(sig, stop.set)
            except NotImplementedError:  # Windows / 非主线程
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
                        # 每拍按最新分配集合 reconcile：分片迁移 / 凭证增删 / 字段刷新 → 起停或重启源实例
                        supervisor.reconcile(desired_source_creds(hb_creds))
                except Exception as e:  # noqa: BLE001
                    log.warning("[sokel] 心跳续约失败: %s", e)
        finally:
            # 优雅下线：通知平台立即标记 offline（秒级感知），而不是等心跳超时清扫（45s+）
            if supervisor is not None:
                supervisor.stop_all()
            try:
                await nc.publish(
                    "sokel.unregister",
                    json.dumps({"token": p.token, "instance_id": instance_id}).encode(),
                )
                await nc.flush()  # 确保下线通知先于断连送达
            except Exception:  # noqa: BLE001
                pass
            log.info("[sokel] 已通知平台下线，退出")
            await nc.drain()

    async def _dispatch(self, p: Plugin, nc: Any, msg: Msg, files: NatsFiles, instance_id: str) -> None:
        if not msg.reply:
            return
        try:
            call = json.loads(msg.data)
        except Exception:  # noqa: BLE001
            await nc.publish(msg.reply, json.dumps({"error": "调用帧解不开"}).encode())
            return
        op = call.get("operation") or ""
        tag = _trace_tag(call.get("trace") or {})

        # 平台代收 webhook 的特殊帧：分发前拦截（老 SDK 没这段会走 unknown operation，
        # 平台把它翻译成「插件未注册 webhook 处理器」）
        if op == C.OP_WEBHOOK:
            log.info("[sokel] ← webhook 入站%s", tag)
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
        log.info("[sokel] ← %s 开始%s", op, tag)
        if p.contract.is_stream(op):
            # 流式：逐帧发布到回复通道，末尾必发终止帧
            async def publish_frame(frame: Dict[str, Any]) -> None:
                await nc.publish(msg.reply, json.dumps(frame).encode(), headers=headers)

            pending: List[asyncio.Task] = []

            def sink(frame: Dict[str, Any]) -> None:
                pending.append(asyncio.create_task(publish_frame(frame)))

            try:
                await p.dispatch(call, sink, files)
                if pending:
                    await asyncio.gather(*pending)
                log.info("[sokel] ✓ %s 完成(%dms)%s", op, _ms(started), tag)
            except Exception as e:  # noqa: BLE001
                log.warning("[sokel] ✗ %s 失败(%dms)%s: %s", op, _ms(started), tag, e)
                await publish_frame({"kind": "error", "text": str(e)})
            await publish_frame({"kind": "end"})
            return

        try:
            vars_ = await p.dispatch_buffered(call, files)
        except Exception as e:  # noqa: BLE001
            log.warning("[sokel] ✗ %s 失败(%dms)%s: %s", op, _ms(started), tag, e)
            await nc.publish(msg.reply, json.dumps({"error": str(e)}).encode(), headers=headers)
            return
        log.info("[sokel] ✓ %s 完成(%dms)%s", op, _ms(started), tag)
        await nc.publish(msg.reply, json.dumps(vars_).encode(), headers=headers)


def _make_supervisor(p: Plugin, nc: Any, files: NatsFiles) -> SourceSupervisor:
    """每个凭证一套源实例：ctx 绑定该凭证，trigger 自动回带其 credential_id。"""

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
    log.info("[sokel] 事件源「%s」启动（credential=%s）", src.id, cred.id or "(无凭证)")
    p.board.set(src.id, cred.id, "running")
    try:
        await src.fn(sctx)
        p.board.set_if_running(src.id, cred.id, "exited")
    except asyncio.CancelledError:
        raise  # reconcile 停止：状态已由 stop() 整体移除
    except Exception as e:  # noqa: BLE001
        log.exception("[sokel] 事件源「%s」退出（credential=%s）", src.id, cred.id or "(无凭证)")
        p.board.set(src.id, cred.id, "error", str(e))


def _resync(register: Any, supervisor: SourceSupervisor) -> None:
    async def go() -> None:
        try:
            _, _, creds = await register()
            supervisor.reconcile(desired_source_creds(creds))
        except Exception as e:  # noqa: BLE001
            log.warning("[sokel] 凭证变更 re-register 失败: %s", e)

    asyncio.create_task(go())


class _Debouncer:
    """合并短窗内的多次触发（批量增删凭证只 re-register 一趟）。"""

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
    """broker 未就绪时挂起等它，而不是启动失败退出（与 Go 的 RetryOnFailedConnect 同义）。"""
    while True:
        try:
            return await nats.connect(**opts)
        except Exception as e:  # noqa: BLE001
            log.warning("[sokel] 连接平台失败（%s），%ds 后重试…", e, RETRY_SEC)
            await asyncio.sleep(RETRY_SEC)


async def discover(endpoint: str, token: str) -> str:
    """统一 https 端点 → 经平台 /connect-info 发现真实承载地址。

    直填 nats:// / tls:// 时跳过发现（本地开发 / 离线场景）。
    """
    ep = (endpoint or "").strip()
    if ep.startswith("nats://") or ep.startswith("tls://"):
        return ep
    if not (ep.startswith("http://") or ep.startswith("https://")):
        raise ValueError(f"端点 {endpoint!r} 不合法：应为平台地址（https://…）或 nats://")
    url = ep.rstrip("/") + "/api/v1/connect-info"
    info = await asyncio.to_thread(_http_get_json, url, token)
    target = (info.get("transports") or {}).get("nats")
    if not target:
        raise RuntimeError("平台未提供可用承载（connect-info.transports 为空）")
    return target


def _http_get_json(url: str, token: str) -> Dict[str, Any]:
    import urllib.request

    req = urllib.request.Request(url, headers={"Authorization": f"Bearer {token}"})
    with urllib.request.urlopen(req, timeout=8) as resp:  # noqa: S310
        return json.loads(resp.read().decode())


def stable_instance_id(token: str) -> str:
    """副本的稳定身份：重启复用，否则平台侧每次重启都多出一行永远 offline 的幽灵实例。

    1) SOKEL_INSTANCE_ID；2) 工作目录里按 token 指纹命名的落盘文件；3) 落盘失败 → host-pid。
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
