"""Typed errors.

Errors used to travel as a bare string both ways: callers matched English text, and a plugin had no way
to tell the platform "this is a credential problem" or "retrying may help". Raise one of the PluginError
subclasses from a handler and its ``code`` travels next to the message (``code`` / ``retryable`` in the
reply, or in a streaming ``error`` frame). The SDK's own failures are SokelError subclasses.
"""

from __future__ import annotations

from typing import Any, Dict


class SokelError(Exception):
    """Base class of everything the SDK raises on purpose."""


class NoTransport(SokelError, RuntimeError):
    """The platform answered that it offers no transport (as opposed to a network failure)."""


class PluginError(SokelError):
    """A handler error with a code for the platform. Use a subclass."""

    code = ""
    retryable = False


class Retryable(PluginError):
    """A transient failure (rate limit, upstream 5xx, timeout): trying again may work."""

    code = "retryable"
    retryable = True


class CredentialInvalid(PluginError):
    """The credential was rejected upstream (revoked, expired, wrong key). Retrying will not help."""

    code = "credential_invalid"


class InvalidInput(PluginError):
    """The input is wrong for this operation. Retrying the same input will not help."""

    code = "invalid_input"


def error_fields(e: BaseException) -> Dict[str, Any]:
    """The code fields for a reply or error frame; empty for an uncoded error."""
    code = getattr(e, "code", "") if isinstance(e, PluginError) else ""
    out: Dict[str, Any] = {}
    if code:
        out["code"] = code
        if getattr(e, "retryable", False):
            out["retryable"] = True
    return out
