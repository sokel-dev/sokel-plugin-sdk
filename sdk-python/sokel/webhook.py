# Copyright 2026 The Sokel Authors
# SPDX-License-Identifier: Apache-2.0

"""Webhooks relayed by the platform: upstream system -> platform /hooks/{token} -> a __webhook__
frame lands here (protocol §7b).

What the handler is responsible for: verifying the upstream signature using the secret in the
credential (every vendor signs differently — the platform does not know the upstream, the plugin
does), parsing the body, pushing typed events with ctx.trigger (which reuses the declared-event
check and the platform's deduplication), and deciding the response (GitLab wants a 2xx, Feishu's URL
verification wants the challenge echoed back).
"""

from __future__ import annotations

import base64
from typing import Any, Dict, Optional

from pydantic import BaseModel


class WebhookRequest(BaseModel):
    """One inbound webhook (the platform has already stripped Cookie and other platform-side headers)."""

    method: str = "POST"
    path: str = ""
    query: str = ""
    headers: Dict[str, str] = {}
    body: bytes = b""

    def header(self, name: str) -> str:
        """Case-insensitive header lookup (HTTP semantics: X-Gitlab-Token and x-gitlab-token both hit)."""
        lowered = name.lower()
        for k, v in self.headers.items():
            if k.lower() == lowered:
                return v
        return ""

    @classmethod
    def from_frame(cls, frame: Dict[str, Any]) -> "WebhookRequest":
        # The body travels as base64 to keep the exact bytes: HMAC-style verification has to see them
        # byte for byte, and re-encoding the JSON would break the signature.
        raw = base64.b64decode(frame.get("body_b64") or "")
        return cls(
            method=frame.get("method") or "POST",
            path=frame.get("path") or "",
            query=frame.get("query") or "",
            headers=frame.get("headers") or {},
            body=raw,
        )


class WebhookResponse(BaseModel):
    """The reply sent back upstream. status=0 plus an error means the plugin failed to handle it
    (the platform translates that into a 5xx)."""

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
    """For the cases that must return a body (Feishu's URL-verification challenge, say)."""
    return WebhookResponse(status=status, body=body.encode())
