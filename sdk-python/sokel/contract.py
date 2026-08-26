# Copyright 2026 The Sokel Authors
# SPDX-License-Identifier: Apache-2.0

"""The runtime view of a contract (wire protocol §5).

A contract is **data**: `sokel-gen` renders a CONTRACT dict from sokel.yaml, and the runtime only
looks things up in it and reports it. So there is no second Field class here — that would be a second
definition of the contract, and two definitions drift. The Go side learned this once: the SDK's Field
was complete while the platform kept its own four-key version, so anything the SDK declared that the
platform's copy lacked was invisible to the platform.
"""

from __future__ import annotations

from typing import Any, Dict, List, Optional

# Keys of the contract dict. They match the registration payload in protocol §3 verbatim, so they
# are reported as-is with no translation step.
KEY_OPERATIONS = "operations"
KEY_CREDENTIAL = "credential_schema"
KEY_EVENTS = "events"
KEY_EVENTS_COMMON = "events_common"
KEY_AUTH_FLOW = "auth_flow"
KEY_OAUTH = "oauth"
KEY_CAPABILITIES = "capabilities"
KEY_DOC = "doc"
KEY_DOC_URL = "doc_url"

# Reserved operation ids (the auth flow). They contain a dot, which business ids cannot produce
# (a business id must match ^[a-z][a-z0-9_]*$).
OP_AUTH_START = "auth.start"
OP_AUTH_POLL = "auth.poll"
OP_AUTH_SUBMIT = "auth.submit"

# The special operation name for platform-relayed webhooks (it reuses the call frame, protocol §7b).
OP_WEBHOOK = "__webhook__"

# Capability bit: registering a webhook handler *is* the declaration; the author does not repeat it.
CAP_WEBHOOK = "webhook"


class Contract:
    """One plugin contract. The constructor takes the generated CONTRACT dict."""

    def __init__(self, data: Optional[Dict[str, Any]] = None) -> None:
        self.data: Dict[str, Any] = dict(data or {})

    # —— lookups ——

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

    # —— reporting ——

    def payload(self) -> Dict[str, Any]:
        """The contract half of the registration payload. Empty values are omitted (the protocol makes
        every new field optional)."""
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
        # operations must be present even when empty: a platform that cannot find the key reads it as
        # "contract locked", not as "this plugin has no operations".
        out.setdefault(KEY_OPERATIONS, [])
        return out
