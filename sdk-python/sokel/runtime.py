"""运行时形状：文件引用、产出帧、调用上下文。

与传输无关——NATS 只提供「取字节 / 存字节 / 发帧」三个回调，剩下的语义都在这里。
测试因此不需要 broker：塞一个假的文件运行时与假的 sink 就能跑完整条分发路径。
"""

from __future__ import annotations

from typing import Any, Awaitable, Callable, Dict, List, Optional, Protocol

from pydantic import BaseModel, Field as _PField


class File(BaseModel):
    """文件引用（画布里只流转引用，不内联字节）。"""

    id: str = ""
    url: str = ""
    name: str = ""
    mime: str = ""
    size: int = 0
    # data：无平台文件层时（单元测试）直接携带的字节，不参与序列化
    data: Optional[bytes] = _PField(default=None, exclude=True)

    async def blob(self, ctx: "Ctx") -> bytes:
        """惰性取字节（经平台文件层分块拉取）。"""
        if self.data is not None:
            return self.data
        return await ctx.fetch(self)


class FileRuntime(Protocol):
    """文件字节的取/存后端，由传输层注入。"""

    async def fetch(self, f: File) -> bytes: ...

    async def store(self, name: str, mime: str, data: bytes) -> File: ...

    async def store_stream(self, name: str, mime: str, src: Any) -> File: ...


# —— 产出帧（对齐协议 §4 的流式帧）——

FRAME_TEXT = "text"
FRAME_JSON = "json"
FRAME_VARS = "variables"


def _to_vars(value: Any) -> Dict[str, Any]:
    """出参 → 契约命名的字典。pydantic 模型按字段名（= 契约名）导出。"""
    if value is None:
        return {}
    if isinstance(value, BaseModel):
        return value.model_dump(mode="json", exclude_none=True)
    if isinstance(value, dict):
        return dict(value)
    raise TypeError(f"输出必须是 pydantic 模型或 dict，收到 {type(value).__name__}")


class Emitter:
    """类型化产出器。多次调用 = 多帧（流式）；非流式由 SDK 缓冲合并成一次回复。"""

    def __init__(self, sink: Callable[[Dict[str, Any]], None]) -> None:
        self._sink = sink

    def text(self, s: str) -> None:
        """人类可读文本（展示 / tracing）。"""
        self._sink({"kind": FRAME_TEXT, "text": s})

    def json(self, v: Any) -> None:
        """结构化 JSON（展示 / tracing）。"""
        self._sink({"kind": FRAME_JSON, "json": v})

    def vars(self, out: Any) -> None:
        """类型化输出变量（进下游节点）。可多次调用，后帧覆盖同名字段。"""
        m = _to_vars(out)
        if m:
            self._sink({"kind": FRAME_VARS, "vars": m})


class BufferSink:
    """非流式汇聚：只收 variables 帧，合并成一个输出对象（text/json 仅流式展示用）。"""

    def __init__(self) -> None:
        self.vars: Dict[str, Any] = {}

    def __call__(self, frame: Dict[str, Any]) -> None:
        if frame.get("kind") != FRAME_VARS:
            return
        self.vars.update(frame.get("vars") or {})


class Ctx:
    """操作 handler 的上下文：凭证、追踪、文件取存。"""

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
        """平台下发的追踪上下文（run_id / workflow_id / node_id）。

        非工作流调用（试调用、健康检查）没有这些值，返回空串——**调用方要把空串当
        「没有重试语义」处理**，而不是当成一个恒定的键（那会把两次独立调用错误地去重）。
        """
        return self._trace.get(key, "")

    def credential_as(self, model: type) -> Any:
        """凭证按类型化模型读出。缺的键走模型默认值——凭证值一律是字符串。"""
        return model(**{k: v for k, v in self.credential.items() if v != ""})

    async def fetch(self, f: File) -> bytes:
        if f.data is not None:
            return f.data
        if self._files is None:
            raise RuntimeError("文件运行时未就绪")
        return await self._files.fetch(f)

    async def upload(self, name: str, mime: str, data: bytes) -> File:
        """产出一个文件：字节交回平台登记，返回可放进出参的引用。"""
        if self._files is None:
            return File(name=name, mime=mime, size=len(data), data=data)
        return await self._files.store(name, mime, data)

    async def upload_file(self, path: str, name: str = "", mime: str = "") -> File:
        """边读边传本地文件：内存占用恒为一个块（1 MiB），与文件大小无关。

        几百 MB 以上的东西（视频、压缩包、数据集）一律走它——upload() 要先把整个文件
        读进内存，那会把插件进程撑爆，而症状是「大文件时容器莫名其妙被 OOM 杀掉」。
        """
        import mimetypes
        import os

        name = name or os.path.basename(path)
        mime = mime or (mimetypes.guess_type(name)[0] or "application/octet-stream")
        if self._files is None:  # 裸 ctx（测试）：读进内存当作直接产出
            with open(path, "rb") as fh:
                data = fh.read()
            return File(name=name, mime=mime, size=len(data), data=data)
        with open(path, "rb") as fh:
            return await self._files.store_stream(name, mime, fh)


Handler = Callable[..., Any]
AsyncHandler = Callable[..., Awaitable[Any]]
