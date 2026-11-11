"""Exercise SDK recovery defaults against an actual TCP connection and NATS client."""

import asyncio
import json
from contextlib import asynccontextmanager

import pytest

from sokel.nats_transport import _connect_forever


@asynccontextmanager
async def broker():
    writers = set()
    state = {"connections": 0, "pings": 0, "drop_first": False, "subscriptions": {}}
    periodic_ping = asyncio.Event()

    async def serve(reader, writer):
        writers.add(writer)
        state["connections"] += 1
        connection = state["connections"]
        subscriptions = state["subscriptions"][connection] = set()
        info = {"server_id": "recovery-test", "version": "2.10.0", "proto": 1, "max_payload": 1048576}
        writer.write(("INFO " + json.dumps(info) + "\r\n").encode())
        try:
            while line := await reader.readline():
                if line == b"PING\r\n":
                    state["pings"] += 1
                    if state["pings"] > 1:
                        periodic_ping.set()
                    if not state["drop_first"] or connection > 1:
                        writer.write(b"PONG\r\n")
                        await writer.drain()
                elif line.startswith(b"SUB "):
                    subscriptions.add(line.split()[1].decode())
        except (ConnectionError, OSError):
            pass
        finally:
            writers.discard(writer)
            writer.close()

    server = await asyncio.start_server(serve, "127.0.0.1", 0)
    url = f"nats://127.0.0.1:{server.sockets[0].getsockname()[1]}"
    try:
        yield url, state, periodic_ping
    finally:
        for writer in list(writers):
            writer.close()
            await writer.wait_closed()
        server.close()
        await server.wait_closed()


async def test_default_keepalive_arrives_before_lease_is_at_risk():
    async with broker() as (url, _, periodic_ping):
        nc = await _connect_forever({"servers": [url]})
        try:
            await asyncio.wait_for(periodic_ping.wait(), 12)
        finally:
            await nc.close()


@pytest.mark.parametrize("network", ["internal", "public"])
async def test_missing_pongs_reconnect_and_restore_subscriptions(network):
    async with broker() as (url, state, _):
        reconnected = asyncio.Event()

        async def on_reconnect():
            reconnected.set()

        nc = await _connect_forever({
            "servers": [url], "ping_interval": 0.02, "reconnect_time_wait": 0.01,
            "reconnected_cb": on_reconnect,
        }, network)
        try:
            await nc.subscribe("plugin.calls")
            await nc.flush(timeout=1)
            state["drop_first"] = True
            await asyncio.wait_for(reconnected.wait(), 2)
            await nc.flush(timeout=1)
            assert "plugin.calls" in state["subscriptions"][2]
        finally:
            await nc.close()


async def test_socket_write_error_uses_native_client_recovery():
    async with broker() as (url, state, _):
        reconnected = asyncio.Event()

        async def on_reconnect():
            reconnected.set()

        async def on_error(_error):
            pass

        nc = await _connect_forever({
            "servers": [url], "reconnect_time_wait": 0.01,
            "reconnected_cb": on_reconnect, "error_cb": on_error,
        })
        original_write = nc._transport.writelines
        try:
            def fail_write(_payload):
                # nats-py reuses its transport adapter across sockets; fail the old socket once.
                nc._transport.writelines = original_write
                raise BrokenPipeError("injected socket write failure")

            nc._transport.writelines = fail_write
            await nc.publish("heartbeat", b"renew")
            await asyncio.wait_for(reconnected.wait(), 2)
            await nc.flush(timeout=1)
            assert state["connections"] == 2
        finally:
            nc._transport.writelines = original_write
            await nc.close()


async def test_request_timeout_keeps_healthy_socket():
    async with broker() as (url, state, _):
        nc = await _connect_forever({"servers": [url]})
        try:
            with pytest.raises(asyncio.TimeoutError):
                await nc.request("no.reply", b"", timeout=0.02)
            await nc.flush(timeout=1)
            assert state["connections"] == 1
        finally:
            await nc.close()
