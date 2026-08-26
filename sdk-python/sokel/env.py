# Copyright 2026 The Sokel Authors
# SPDX-License-Identifier: Apache-2.0

"""One place to read the plugin's environment variables (mirrors pluginenv on the Go side).

There is no compatibility layer for a second prefix: accepting one saves a single redeploy and buys
a piece of history nobody dares remove.
"""

from __future__ import annotations

import os

PREFIX = "SOKEL_"


def get(name: str) -> str:
    """Read SOKEL_<name>. `name` carries no prefix, e.g. get("TOKEN")."""
    return (os.environ.get(PREFIX + name) or "").strip()


def get_or(name: str, default: str) -> str:
    return get(name) or default
