import asyncio
import json
import inspect

import pytest

from sokel.nats_transport import _connect_forever
from sokel import nats_transport
from nats.aio.client import Client


@pytest.mark.parametrize("network", ["internal", "public"])
async def test_info_and_reconnect_keep_network_policy(network):
    writers = set()
    info = {
        "server_id": "broker-test", "version": "2.10.0", "proto": 1, "headers": True,
        "max_payload": 1048576, "connect_urls": ["127.0.0.1:65530"],
    }

    async def serve(reader, writer):
        writers.add(writer)
        writer.write(("INFO " + json.dumps(info) + "\r\n").encode())
        try:
            while line := await reader.readline():
                if line == b"PING\r\n":
                    writer.write(b"PONG\r\n")
                    await writer.drain()
        finally:
            writers.discard(writer)
            writer.close()

    server = await asyncio.start_server(serve, "127.0.0.1", 0)
    address = f"127.0.0.1:{server.sockets[0].getsockname()[1]}"
    reconnected = asyncio.Event()
    lame_duck = asyncio.Event()

    async def on_reconnect():
        reconnected.set()

    async def on_lame_duck():
        lame_duck.set()

    nc = None
    try:
        supports_lame_duck = "lame_duck_mode_cb" in inspect.signature(Client.connect).parameters
        options = {
            "servers": [f"nats://{address}"], "dont_randomize": True,
            "reconnect_time_wait": 0.01, "reconnected_cb": on_reconnect,
        }
        if supports_lame_duck:
            options["lame_duck_mode_cb"] = on_lame_duck
        nc = await asyncio.wait_for(_connect_forever(options, network), 3)
        assert {s.netloc for s in nc.servers} == ({address} if network == "internal" else {address, "127.0.0.1:65530"})
        assert bool(nc.discovered_servers) == (network == "public")
        assert nc.max_payload == 1048576

        for writer in writers:
            writer.write(("INFO " + json.dumps({**info, "ldm": True, "connect_urls": ["127.0.0.1:65529"]}) + "\r\n").encode())
        await nc.flush(timeout=2)
        if supports_lame_duck:
            assert lame_duck.is_set(), "ordinary INFO notifications still work"
        assert {s.netloc for s in nc.servers} == ({address} if network == "internal" else {address, "127.0.0.1:65530", "127.0.0.1:65529"})

        if network == "internal":
            for writer in list(writers):
                writer.close()
            await asyncio.wait_for(reconnected.wait(), 3)
            assert nc.connected_url.netloc == address
            assert {s.netloc for s in nc.servers} == {address}
            await nc.flush(timeout=2)
    finally:
        if nc is not None:
            await nc.close()
        for writer in list(writers):
            writer.close()
            await writer.wait_closed()
        server.close()
        await server.wait_closed()


async def test_failed_internal_connection_is_closed_before_retry(monkeypatch):
    clients = []

    class Client:
        def __init__(self):
            self.closed = False
            clients.append(self)

        async def connect(self, **opts):
            if len(clients) == 1:
                raise OSError("broker unavailable")

        async def close(self):
            self.closed = True

    monkeypatch.setattr(nats_transport, "InternalBrokerClient", Client)
    monkeypatch.setattr(nats_transport, "RETRY_SEC", 0)
    assert await _connect_forever({}, "internal") is clients[1]
    assert clients[0].closed
    assert not clients[1].closed


async def test_cancelled_internal_connect_closes_client(monkeypatch):
    closed = []

    class Client:
        async def connect(self, **opts):
            raise asyncio.CancelledError()

        async def close(self):
            closed.append(True)

    monkeypatch.setattr(nats_transport, "InternalBrokerClient", Client)
    with pytest.raises(asyncio.CancelledError):
        await _connect_forever({}, "internal")
    assert closed == [True]
