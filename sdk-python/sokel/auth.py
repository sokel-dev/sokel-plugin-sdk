# Copyright 2026 The Sokel Authors
# SPDX-License-Identifier: Apache-2.0

"""Collaborative credential authentication: some credentials cannot be typed in by hand — a QR scan,
a verification code, an OAuth consent page.

    the panel's "log in" button -> start returns a challenge -> (scan / type it back)
    -> poll every 2s -> confirmed

The shape is declared in manifest.yml under credential.auth; the handlers hang off the reserved
operation ids auth.start / auth.poll / auth.submit. **Do not** register a business operation named
auth_start: those three names were never reserved, so any plugin with an operation of that name made
the panel's button appear out of nowhere.
"""

from __future__ import annotations

from typing import Any, Dict, Optional

from pydantic import BaseModel

KIND_QR = "qr"
KIND_INPUT = "input"
KIND_OAUTH = "oauth"

# Status constants: a misspelled string raises nothing, it just leaves the panel spinning forever.
PENDING = "pending"
SCANNED = "scanned"
CONFIRMED = "confirmed"
EXPIRED = "expired"


class AuthChallenge(BaseModel):
    """What start hands back. The panel renders by kind: qr draws a code, input shows the prompt."""

    auth_id: str = ""
    kind: str = ""
    qr_image: str = ""  # data-uri
    prompt: str = ""
    expires_in: int = 0


class AuthState(BaseModel):
    """The result of poll. Carry `session` only once confirmed — handing it over earlier makes the
    platform rewrite the credential row again and again."""

    status: str = PENDING
    session: Optional[Dict[str, Any]] = None
