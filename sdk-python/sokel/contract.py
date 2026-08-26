"""契约的运行时视图（线协议 §5）。

契约本身是**数据**：`sokel-gen` 从 sokel.yaml 生成一份 CONTRACT 字典，运行时只是查它、上报它。
所以这里不重新定义一套 Field 类——那会变成契约的第二份定义，而两份定义迟早会漂
（Go 侧栽过一次：SDK 的 Field 是全量的、平台那份只有四个键，SDK 声明了的东西平台看不见）。
"""

from __future__ import annotations

from typing import Any, Dict, List, Optional

# 契约字典的键（与线协议 §3 的注册载荷同名，直接上报，不做转换）
KEY_OPERATIONS = "operations"
KEY_CREDENTIAL = "credential_schema"
KEY_EVENTS = "events"
KEY_EVENTS_COMMON = "events_common"
KEY_AUTH_FLOW = "auth_flow"
KEY_OAUTH = "oauth"
KEY_CAPABILITIES = "capabilities"
KEY_DOC = "doc"
KEY_DOC_URL = "doc_url"

# 保留操作 id（认证流）。带点号，业务 id 产生不出来（业务 id 限定 ^[a-z][a-z0-9_]*$）。
OP_AUTH_START = "auth.start"
OP_AUTH_POLL = "auth.poll"
OP_AUTH_SUBMIT = "auth.submit"

# 平台代收 webhook 的特殊操作名（复用调用帧，见协议 §7b）
OP_WEBHOOK = "__webhook__"

# 能力位：注册了 webhook 处理器就是支持，不靠作者手动声明
CAP_WEBHOOK = "webhook"


class Contract:
    """一份插件契约。构造参数就是生成物 CONTRACT 字典。"""

    def __init__(self, data: Optional[Dict[str, Any]] = None) -> None:
        self.data: Dict[str, Any] = dict(data or {})

    # —— 查 ——

    def operations(self) -> List[Dict[str, Any]]:
        return list(self.data.get(KEY_OPERATIONS) or [])

    def operation(self, op_id: str) -> Optional[Dict[str, Any]]:
        for op in self.operations():
            if op.get("id") == op_id:
                return op
        return None

    def is_stream(self, op_id: str) -> bool:
        op = self.operation(op_id)
        return bool(op and op.get("stream"))

    def event_ids(self) -> List[str]:
        return [e.get("id", "") for e in (self.data.get(KEY_EVENTS) or [])]

    def get(self, key: str, default: Any = None) -> Any:
        return self.data.get(key, default)

    def set(self, key: str, value: Any) -> None:
        self.data[key] = value

    # —— 上报 ——

    def payload(self) -> Dict[str, Any]:
        """契约部分的注册载荷。空值一律省略（协议：新字段一律 optional）。"""
        out: Dict[str, Any] = {}
        for key in (
            KEY_OPERATIONS,
            KEY_CREDENTIAL,
            KEY_EVENTS,
            KEY_EVENTS_COMMON,
            KEY_AUTH_FLOW,
            KEY_OAUTH,
            KEY_CAPABILITIES,
            KEY_DOC,
            KEY_DOC_URL,
        ):
            v = self.data.get(key)
            if v:
                out[key] = v
        # operations 必须在（哪怕空）：平台侧读不到这个键会当成「契约锁定」而不是「没有操作」
        out.setdefault(KEY_OPERATIONS, [])
        return out
