"""Wire protocol negotiation.

The handshake used to carry no protocol version, so when the platform changed the protocol an older SDK
just logged "registration failed … retrying" forever and never said "upgrade the SDK".
"""

import pytest
from conftest import SIMPLE_CONTRACT

from sokel import Plugin, SDKTooOld
from sokel.protocol import WIRE_PROTOCOL, check_protocol


def test_registration_reports_protocol_and_sdk():
    body = Plugin(dict(SIMPLE_CONTRACT), name="demo", token="t").register_payload("i", "h", "t")
    assert body["protocol"] == WIRE_PROTOCOL
    assert body["sdk"].startswith("python/")


def test_check_protocol():
    check_protocol({"min_protocol": WIRE_PROTOCOL})
    check_protocol({})  # an older platform sends no min_protocol
    with pytest.raises(SDKTooOld, match="upgrade"):
        check_protocol({"min_protocol": WIRE_PROTOCOL + 1})
