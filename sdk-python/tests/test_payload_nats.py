"""Optional companion to the Go integration broker, using the actual nats-py client."""
import asyncio
import hashlib
import json
import os

import nats
import pytest
from conftest import SIMPLE_CONTRACT
from sokel import Plugin
from sokel.nats_transport import NatsFiles, NatsTransport
from sokel.payload import fetch_payload


@pytest.mark.skipif(not os.getenv("SOKEL_TEST_NATS_URL"), reason="requires an isolated integration broker")
async def test_real_nats_large_request_reply_and_ordered_stream():
    nc = await nats.connect(os.environ["SOKEL_TEST_NATS_URL"])
    caller = await nats.connect(os.environ["SOKEL_TEST_NATS_URL"])
    group = "python_test"
    subject = f"sokel.plugin.demo.{group}"
    text = "雪" * 50000
    p = Plugin(SIMPLE_CONTRACT, name="demo", token="t")

    async def greet(ctx, raw, out):
        out.vars({"text": raw["who"]})

    async def stream(ctx, raw, out):
        out.text(text)
        out.text("next")

    p.register("greet", greet)
    p.register("stream_it", stream)

    async def on_call(msg):
        await NatsTransport()._dispatch(p, nc, msg, NatsFiles(nc, "t"), "test-instance")

    await nc.subscribe(subject, cb=on_call)
    await nc.subscribe(subject + ".large", cb=on_call)
    await nc.flush()
    try:
        call = json.dumps({"operation": "greet", "input": {"who": text}, "payload_protocol": 1, "deadline_ms": 5000}).encode()
        chunk_bytes = min(1024 * 1024, caller.max_payload - 1024)

        async def on_chunk(msg):
            offset = json.loads(msg.data)["offset"]
            await msg.respond(call[offset:offset + chunk_bytes])

        source_subject = f"sokel.payload.{group}.input"
        source = await caller.subscribe(source_subject, cb=on_chunk)
        await caller.flush()
        reference = {"__sokel_payload": {"version": 1, "subject": source_subject, "size": len(call), "sha256": hashlib.sha256(call).hexdigest(), "chunk_bytes": chunk_bytes}}
        reply = await caller.request(subject + ".large", json.dumps(reference).encode(), timeout=5)
        body = await fetch_payload(caller, group, reply.data) if (reply.headers or {}).get("Sokel-Payload") == "1" else reply.data
        assert json.loads(body) == {"text": text}
        assert reply.headers["Sokel-Instance"] == "test-instance"
        await source.unsubscribe()

        inbox = caller.new_inbox()
        messages = await caller.subscribe(inbox)
        await caller.flush()
        await caller.publish(subject, json.dumps({"operation": "stream_it", "payload_protocol": 1, "deadline_ms": 5000}).encode(), reply=inbox)
        frames = []
        while True:
            msg = await messages.next_msg(timeout=5)
            body = await fetch_payload(caller, group, msg.data) if (msg.headers or {}).get("Sokel-Payload") == "1" else msg.data
            frame = json.loads(body)
            frames.append(frame)
            if frame["kind"] == "end":
                break
        await messages.unsubscribe()
        assert frames == [{"kind": "text", "text": text}, {"kind": "text", "text": "next"}, {"kind": "end"}]
    finally:
        await caller.close()
        await nc.close()
