"""Plugin wire protocol version (see docs/plugin-wire-protocol.md in the platform repository).

It only goes up for changes both sides must make together:

    1 = the original protocol (replies on the global _INBOX.)
    2 = per-group reply prefix (inbox_prefix, v0.5.5)

The number is reported at registration, and the platform's min_protocol (sent with the access
credentials) is checked before connecting, so an SDK that is too old stops with an upgrade hint
instead of failing to register forever.
"""

from __future__ import annotations

from typing import Any, Mapping

from .errors import SokelError

WIRE_PROTOCOL = 2


class SDKTooOld(SokelError, RuntimeError):
    """The platform requires a newer wire protocol. Waiting does not fix it; rebuilding with a newer SDK does."""


def sdk_ident() -> str:
    from . import __version__

    return f"python/{__version__}"


def check_protocol(acc: Mapping[str, Any]) -> None:
    need = int(acc.get("min_protocol") or 0)
    if need > WIRE_PROTOCOL:
        raise SDKTooOld(
            f"the platform needs wire protocol {need}, this SDK ({sdk_ident()}) speaks {WIRE_PROTOCOL}"
            " — upgrade sokel-plugin-sdk and redeploy"
        )
