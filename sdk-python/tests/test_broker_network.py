import pytest
from types import SimpleNamespace

from conftest import SIMPLE_CONTRACT
from sokel import Plugin

from sokel import nats_transport


ACCESS = {
    "url": "tls://broker.example.com:4222",
    "internal_url": "tls://broker.sokel.svc:4222",
    "user": "group", "pass": "test-pass", "ca": "test-ca",
    "inbox_prefix": "_INBOX_G.group", "subject": "sokel.plugin.demo.group", "min_protocol": 1,
}


async def discovery(monkeypatch, network, body):
    monkeypatch.setenv("SOKEL_BROKER_NETWORK", network)

    def get_json(url, token):
        assert url == "https://platform.example.com/api/v1/connect-info"
        assert token == "test-token"
        return body

    monkeypatch.setattr(nats_transport, "_http_get_json", get_json)
    return await nats_transport.discover("https://platform.example.com", "test-token")


async def test_internal_discovery_preserves_credentials(monkeypatch):
    assert await discovery(monkeypatch, "internal", {"nats": ACCESS}) == {
        **ACCESS, "url": ACCESS["internal_url"],
    }


@pytest.mark.parametrize("network", ["", "public"])
async def test_default_and_public_compatibility(monkeypatch, network):
    assert await discovery(monkeypatch, network, {"nats": ACCESS}) == ACCESS
    assert await discovery(monkeypatch, network, {"transports": {"nats": ACCESS["url"]}}) == {
        "url": ACCESS["url"],
    }


@pytest.mark.parametrize("network", ["auto", "INTERNAL", "internal,public"])
async def test_invalid_network(monkeypatch, network):
    with pytest.raises(ValueError, match="SOKEL_BROKER_NETWORK.*public.*internal"):
        await discovery(monkeypatch, network, {"nats": ACCESS})


@pytest.mark.parametrize("body", [
    {"nats": {"url": ACCESS["url"]}}, {"transports": {"nats": ACCESS["url"]}},
    {"nats": {**ACCESS, "internal_url": ""}}, {"nats": {**ACCESS, "internal_url": None}},
    {"nats": "invalid"}, {"nats": []},
])
async def test_internal_never_falls_back(monkeypatch, body):
    with pytest.raises(ValueError, match="internal_url"):
        await discovery(monkeypatch, "internal", body)


@pytest.mark.parametrize("target", [
    "https://broker.svc:4222", "nats://", "nats://:4222", "nats://broker:0",
    "nats://broker:65536", "nats://broker:bad", "nats://broker:4222/path", "nats://broker:4222?x=1",
    "nats://broker:4222#fragment", "nats://user:pass@broker:4222", "nats://a:4222,nats://b:4222",
    " nats://broker:4222", "nats://broker:4222\n", "nats://broker host:4222", "nats://@broker:4222",
    "nats://broker:", "nats://broker:4222?", "nats://broker:4222#", "nats://[broken]:4222", 42,
])
async def test_internal_rejects_invalid_targets(monkeypatch, target):
    with pytest.raises(ValueError, match="internal_url"):
        await discovery(monkeypatch, "internal", {"nats": {**ACCESS, "internal_url": target}})


@pytest.mark.parametrize("target", ["nats://broker:4222", "tls://broker.svc", "tls://[::1]:4222"])
async def test_valid_internal_targets(monkeypatch, target):
    assert (await discovery(monkeypatch, "internal", {"nats": {**ACCESS, "internal_url": target}}))["url"] == target


async def test_internal_requires_platform_discovery(monkeypatch):
    monkeypatch.setenv("SOKEL_BROKER_NETWORK", "internal")
    with pytest.raises(ValueError, match="internal.*platform|platform.*internal"):
        await nats_transport.discover("nats://broker:4222", "test-token")


@pytest.mark.parametrize("endpoint", ["nats://broker:4222", "tls://broker:4222"])
async def test_public_preserves_direct_endpoints(endpoint):
    assert await nats_transport.discover(endpoint, "test-token", "public") == {"url": endpoint}


async def test_transport_passes_network_and_preserves_connection_credentials(monkeypatch):
    monkeypatch.setenv("SOKEL_BROKER_NETWORK", "internal")
    monkeypatch.setattr(nats_transport, "_http_get_json", lambda *_: {"nats": ACCESS})
    loaded_ca = []
    tls = SimpleNamespace(load_verify_locations=lambda **kw: loaded_ca.append(kw))
    monkeypatch.setattr("ssl.create_default_context", lambda: tls)

    class Connected(Exception):
        pass

    async def connect(opts, network):
        assert network == "internal"
        assert opts["servers"] == [ACCESS["internal_url"]]
        assert opts["user"] == ACCESS["user"]
        assert opts["password"] == ACCESS["pass"]
        assert opts["inbox_prefix"] == ACCESS["inbox_prefix"]
        assert opts["tls"] is tls
        assert loaded_ca == [{"cadata": ACCESS["ca"]}]
        raise Connected()

    monkeypatch.setattr(nats_transport, "_connect_forever", connect)
    p = Plugin(dict(SIMPLE_CONTRACT), name="demo", endpoint="https://platform.example.com", token="test-token")
    with pytest.raises(Connected):
        await nats_transport.NatsTransport().run(p)
