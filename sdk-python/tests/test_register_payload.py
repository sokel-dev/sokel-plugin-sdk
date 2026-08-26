"""注册握手：声明了却没上报，是这套自报机制最典型的静默失效。

插件侧一切正常、平台侧什么也没发生，作者只能对着一个没反应的界面排查——
所以每一项契约都要有一条断言盯着它。
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
            "doc": "# 说明",
        }
    )
    p = Plugin(contract, name="demo", token="skp_x", version="1.2.3")
    body = p.register_payload("inst-1", "host-1", "2026-01-01T00:00:00Z")

    assert body["token"] == "skp_x"
    assert body["instance_id"] == "inst-1"
    assert body["transport"] == "nats"
    assert body["version"] == "1.2.3"
    for key in ("operations", "credential_schema", "events", "events_common", "auth_flow", "capabilities", "doc"):
        assert body.get(key), f"契约的 {key} 没进注册载荷"


def test_webhook_registration_reports_the_capability():
    """能力位不靠自报靠事实：注册了处理器就是支持，作者忘声明不该让入口按钮消失。"""
    p = Plugin(dict(SIMPLE_CONTRACT), name="demo", token="t")
    assert not p.register_payload("i", "h", "t").get("capabilities")
    p.register_webhook(lambda ctx, req: ok())
    assert p.register_payload("i", "h", "t")["capabilities"]["webhook"] is True


def test_kitchen_sink_contract_equals_golden():
    """参考插件：Python 生成物内嵌的契约必须等于那份 golden（Go / Node 比的是同一份）。"""
    from sokel_gen import CONTRACT

    assert CONTRACT == golden()
