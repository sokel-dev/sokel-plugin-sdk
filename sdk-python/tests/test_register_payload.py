"""The registration handshake: declared but not reported is the classic silent failure of any
self-reporting mechanism.

Everything looks fine on the plugin side, nothing happens on the platform side, and the author is left
debugging an unresponsive screen — so every part of the contract gets an assertion watching it.
"""

from conftest import SIMPLE_CONTRACT, golden

from sokel import Plugin, WebhookRequest, ok


def test_payload_carries_every_contract_part():
    contract = dict(SIMPLE_CONTRACT)
    contract.update(
        {
            "credential_schema": [{"name": "api_key", "type": "secret"}],
            "events_common": [{"name": "at", "type": "string"}],
            "auth_flow": {"kind": "qr", "steps": ["start", "poll"]},
            "capabilities": {"recency": False},
            "doc": "# Guide",
        }
    )
    p = Plugin(contract, name="demo", token="skp_x", version="1.2.3")
    body = p.register_payload("inst-1", "host-1", "2026-01-01T00:00:00Z")

    assert body["token"] == "skp_x"
    assert body["instance_id"] == "inst-1"
    assert body["transport"] == "nats"
    assert body["version"] == "1.2.3"
    for key in ("operations", "credential_schema", "events", "events_common", "auth_flow", "capabilities", "doc"):
        assert body.get(key), f"the contract's {key} did not reach the registration payload"


def test_webhook_registration_reports_the_capability():
    """Capability bits follow the facts rather than a declaration: registering a handler means support, and
    forgetting to declare it should not make the entry button disappear."""
    p = Plugin(dict(SIMPLE_CONTRACT), name="demo", token="t")
    assert not p.register_payload("i", "h", "t").get("capabilities")
    p.register_webhook(lambda ctx, req: ok())
    assert p.register_payload("i", "h", "t")["capabilities"]["webhook"] is True


def test_kitchen_sink_contract_equals_golden():
    """The reference plugin: the contract embedded in the Python output must equal the golden file, which is
    the same one Go and Node compare against."""
    from sokel_gen import CONTRACT

    assert CONTRACT == golden()
