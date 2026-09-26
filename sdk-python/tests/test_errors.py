"""Typed errors travel as code / retryable next to the message (F-328)."""

from sokel import CredentialInvalid, InvalidInput, NoTransport, Retryable, SDKTooOld, SokelError
from sokel.errors import error_fields


def test_codes():
    assert error_fields(Retryable("429")) == {"code": "retryable", "retryable": True}
    assert error_fields(CredentialInvalid("401")) == {"code": "credential_invalid"}
    assert error_fields(InvalidInput("bad chat_id")) == {"code": "invalid_input"}
    assert error_fields(ValueError("plain")) == {}


def test_sdk_errors_share_a_base():
    for cls in (NoTransport, SDKTooOld, Retryable):
        assert issubclass(cls, SokelError)
    # still RuntimeError where it used to be, so existing `except RuntimeError` keeps working
    assert issubclass(NoTransport, RuntimeError) and issubclass(SDKTooOld, RuntimeError)
