"""平台代插件收 webhook：外部系统 → 平台 /hooks/{token} → __webhook__ 帧到这里（协议 §7b）。

handler 的职责：用凭证里的 secret 验上游签名（各家算法不同，平台不懂上游、插件懂）→
解析 body → ctx.trigger 推 typed 事件（走既有声明校验与平台去重）→ 返回响应
（GitLab 要 2xx、飞书 URL 校验要回 challenge，由 handler 决定）。
"""

from __future__ import annotations

import base64
from typing import Any, Dict, Optional

from pydantic import BaseModel


class WebhookRequest(BaseModel):
    """一次入站 webhook（平台已剥掉 Cookie 等平台侧头）。"""

    method: str = "POST"
    path: str = ""
    query: str = ""
    headers: Dict[str, str] = {}
    body: bytes = b""

    def header(self, name: str) -> str:
        """大小写不敏感取头（HTTP 语义；X-Gitlab-Token 与 x-gitlab-token 都认）。"""
        lowered = name.lower()
        for k, v in self.headers.items():
            if k.lower() == lowered:
                return v
        return ""

    @classmethod
    def from_frame(cls, frame: Dict[str, Any]) -> "WebhookRequest":
        # body 走 base64 保原始字节：HMAC 类验签必须逐字节一致，JSON 重编码会破坏签名
        raw = base64.b64decode(frame.get("body_b64") or "")
        return cls(
            method=frame.get("method") or "POST",
            path=frame.get("path") or "",
            query=frame.get("query") or "",
            headers=frame.get("headers") or {},
            body=raw,
        )


class WebhookResponse(BaseModel):
    """回给上游的应答。status=0 + error 表示处理失败（平台翻译成 5xx）。"""

    status: int = 200
    headers: Optional[Dict[str, str]] = None
    body: bytes = b""

    def to_frame(self, events: int = 0) -> Dict[str, Any]:
        return {
            "status": self.status or 200,
            "headers": self.headers,
            "body_b64": base64.b64encode(self.body).decode(),
            "events": events,
        }


def ok() -> WebhookResponse:
    return WebhookResponse(status=200)


def text(status: int, body: str) -> WebhookResponse:
    """要回 body 的场景（飞书 URL 校验的 challenge 这类）。"""
    return WebhookResponse(status=status, body=body.encode())
