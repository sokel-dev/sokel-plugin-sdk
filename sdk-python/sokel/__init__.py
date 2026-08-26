# Copyright 2026 The Sokel Authors
# SPDX-License-Identifier: Apache-2.0

"""Sokel plugin SDK for Python.

The contract is declared in a language-neutral sokel.yaml; `sokel-gen generate` turns it into typed
models and registration functions. This package is the runtime: registration handshake, heartbeat,
call dispatch, chunked file transfer, event triggering, webhooks and collaborative authentication.

    from sokel import Plugin
    from sokel_gen import CONTRACT, on_issues_list, IssuesListIn, IssuesListOut

    p = Plugin(CONTRACT, name="gitlab")

    async def handle(ctx, in_: IssuesListIn) -> IssuesListOut:
        ...

    on_issues_list(p, handle)
    asyncio.run(p.run())
"""

from .auth import AuthChallenge, AuthState
from .contract import Contract
# The exported name must not be `env`: that would shadow the sokel.env submodule, and `from . import
# env` inside the package would bind the function instead of the module. The first example run hit
# exactly that, and the error it raised said nothing useful about the cause.
from .env import get as getenv, get_or as getenv_or
from .events import CredEntry, Source, SourceCtx, StateBoard
from .plugin import Plugin
from .runtime import Ctx, Emitter, File
from .webhook import WebhookRequest, WebhookResponse, ok, text

__all__ = [
    "AuthChallenge",
    "AuthState",
    "Contract",
    "CredEntry",
    "Ctx",
    "Emitter",
    "File",
    "Plugin",
    "Source",
    "SourceCtx",
    "StateBoard",
    "WebhookRequest",
    "WebhookResponse",
    "getenv",
    "getenv_or",
    "ok",
    "text",
]

# The version comes from the **installed distribution metadata**; it is not written out again here.
# A hard-coded copy would be the fourth place the version lives (the other three: pyproject,
# package.json, git tag) and it is the one most easily missed — 0.2.0 shipped with this line still
# saying 0.1.0, so the distribution said one thing and the module said another.
try:
    from importlib.metadata import version as _dist_version

    __version__ = _dist_version("sokel-plugin-sdk")
except Exception:  # running straight from the source tree (not installed) has no metadata
    __version__ = "0.0.0+local"
