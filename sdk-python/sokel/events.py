"""事件源：插件主动把外部事件推给平台起 workflow（协议 §7）。

与操作的区别：操作是 request/reply（平台调插件），事件是 fire-and-forget（插件推平台）。

多 bot 单实例（协议 v1.3）：平台每次注册/心跳下发「分配给本副本的凭证子集」，
supervisor 按它 reconcile —— 每个凭证一套源实例，凭证被移除就取消，字段变了就重启。
"""

from __future__ import annotations

import asyncio
import json
import logging
from typing import Any, Awaitable, Callable, Dict, List, Optional

from pydantic import BaseModel

from .runtime import File, FileRuntime, _to_vars

log = logging.getLogger("sokel")

TRIGGER_SUBJECT = "sokel.trigger"
CREDENTIAL_UPDATE_SUBJECT = "sokel.credential.update"


class CredEntry:
    """注册回包 credentials 列表项 —— 分配给本副本的一个 bot 身份。"""

    def __init__(self, id: str = "", fields: Optional[Dict[str, str]] = None) -> None:
        self.id = id
        self.fields = dict(fields or {})

    def sig(self) -> str:
        """字段的稳定签名：reconcile 据此判定「字段变更 → 重启该源实例」。"""
        return "\n".join(f"{k}={self.fields[k]}" for k in sorted(self.fields))


class StateBoard:
    """源实例运行态（源 × 凭证）。随注册/心跳上报，面板据此展示每个 bot。"""

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
        """只在该实例仍是 running 时改写。

        源自报过状态（如 auth_required）之后正常返回，收尾时不该把那句话盖掉——
        盖掉之后面板上只剩一个「已退出」，而「为什么退出」正是要看的那一半。
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
    """常驻事件源 / webhook 的上下文：推事件、读凭证、回写凭证、上传附件、自报状态。"""

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
        # stopping：该源实例被 reconcile 停止时置位。长轮询循环 while not ctx.stopping.is_set()。
        self.stopping = stopping or asyncio.Event()

    def credential_as(self, model: type) -> Any:
        return model(**{k: v for k, v in self.credential.items() if v != ""})

    async def trigger(self, event: str, event_id: str, payload: Any) -> None:
        """推一条事件（fire-and-forget）。

        event 必须是已声明的事件 id —— 拼错在这里当场报错，而不是变成一条平台侧
        无人认领的消息（那种失败没有任何症状：插件日志正常，工作流就是不起）。
        event_id 是幂等键，平台按 (plugin, event, event_id) 去重。
        """
        if self._valid and event not in self._valid:
            raise ValueError(f"未声明的事件 {event!r}（先在 sokel.yaml 的 events 里声明）")
        msg: Dict[str, Any] = {"token": self._token, "event": event, "payload": _to_vars(payload)}
        if event_id:
            msg["event_id"] = event_id
        if self.credential_id:
            msg["credential_id"] = self.credential_id
        await self._publish(TRIGGER_SUBJECT, json.dumps(msg).encode())

    async def update_credential(self, patch: Dict[str, str]) -> None:
        """把 patch 回写到本实例绑定的平台凭证（会话型凭证运行中刷新用）。

        平台是唯一凭证存储方，插件本地从不落地凭证。
        """
        if not self.credential_id:
            raise RuntimeError("本源实例未绑定凭证，无可回写目标")
        if not patch:
            return
        self.credential.update(patch)
        data = json.dumps(
            {"token": self._token, "credential_id": self.credential_id, "patch": patch}
        ).encode()
        await self._publish(CREDENTIAL_UPDATE_SUBJECT, data)

    def report_status(self, status: str, msg: str = "") -> None:
        """自报运行态（如 session 失效 → auth_required），随心跳上报，面板亮「待登录」。"""
        if self._board is not None:
            self._board.set(self.source_id, self.credential_id, status, msg)

    async def upload(self, name: str, mime: str, data: bytes) -> File:
        if self._files is None:
            return File(name=name, mime=mime, size=len(data), data=data)
        return await self._files.store(name, mime, data)

    async def fetch(self, f: File) -> bytes:
        if f.data is not None:
            return f.data
        if self._files is None:
            raise RuntimeError("文件运行时未就绪")
        return await self._files.fetch(f)


class Source:
    """一个常驻事件源。fn 在 SDK 起的 task 里跑，内部用 ctx.trigger 推事件。"""

    def __init__(self, id: str, label: str, fn: Callable[[SourceCtx], Awaitable[None]]) -> None:
        self.id = id
        self.label = label
        self.fn = fn


class SourceSupervisor:
    """per-credential 源实例监督器：按平台下发的凭证集合起停/重启。"""

    def __init__(self, start: Callable[[CredEntry], Callable[[], None]]) -> None:
        self._start = start
        self._running: Dict[str, Any] = {}  # cred_id -> (stop, sig)

    def reconcile(self, desired: List[CredEntry]) -> None:
        want = {c.id: c for c in desired}
        # 停：不在期望集合，或字段变更（先停后起 = 重启）
        for cid in list(self._running):
            stop, sig = self._running[cid]
            c = want.get(cid)
            if c is not None and c.sig() == sig:
                continue
            stop()
            del self._running[cid]
        # 起：期望但未运行
        for cid, c in want.items():
            if cid in self._running:
                continue
            self._running[cid] = (self._start(c), c.sig())

    def stop_all(self) -> None:
        for stop, _ in self._running.values():
            stop()
        self._running.clear()


def desired_source_creds(creds: List[CredEntry]) -> List[CredEntry]:
    """空（无凭证插件）→ 一个空凭证裸实例，与有凭证时同一条代码路径。"""
    return creds or [CredEntry()]
