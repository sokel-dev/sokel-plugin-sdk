import hashlib
import json
import asyncio
import time
from types import SimpleNamespace
import pytest

from conftest import SIMPLE_CONTRACT
from sokel import Plugin
from sokel.nats_transport import NatsFiles, NatsTransport
from sokel.payload import fetch_payload, publish_payload, PayloadError


def encode(value):
    return json.dumps(value).encode()


class Broker:
    max_payload = 4096

    def __init__(self):
        self.published = []
        self.fetched = []
        self.input = b""
        self.subscriptions = {}
        self.received = []
        self.wire_headers = []
        self.consumers = []
        self.cancelled = False

    async def request(self, subject, data, **kwargs):
        request = json.loads(data)
        if request.get("cancel"):
            self.cancelled = True
            return SimpleNamespace(data=b"")
        if subject in self.subscriptions:
            response = asyncio.get_running_loop().create_future()

            async def respond(chunk):
                response.set_result(SimpleNamespace(data=chunk))

            await self.subscriptions[subject](SimpleNamespace(data=data, respond=respond))
            return await response
        offset = request["offset"]
        self.fetched.append(offset)
        return SimpleNamespace(data=self.input[offset:offset + 1024])

    async def publish(self, subject, data, **kwargs):
        if len(data) > self.max_payload:
            raise ValueError("MAX_PAYLOAD_EXCEEDED")
        value = json.loads(data)
        self.published.append(value)
        self.wire_headers.append(kwargs.get("headers"))
        if (kwargs.get("headers") or {}).get("Sokel-Payload") == "1":
            async def consume():
                ref = value["__sokel_payload"]
                chunks, offset = [], 0
                while offset < ref["size"]:
                    response = await self.request(ref["subject"], encode({"offset": offset}))
                    assert len(response.data) == min(ref["chunk_bytes"], ref["size"] - offset)
                    chunks.append(response.data)
                    offset += len(response.data)
                body = b"".join(chunks)
                assert hashlib.sha256(body).hexdigest() == ref["sha256"]
                self.received.append(json.loads(body))

            self.consumers.append(asyncio.create_task(consume()))
        else:
            self.received.append(value)

    async def subscribe(self, subject, cb, **kwargs):
        self.subscriptions[subject] = cb

        async def unsubscribe():
            self.subscriptions.pop(subject, None)

        return SimpleNamespace(unsubscribe=unsubscribe)

    async def flush(self):
        pass


def plugin():
    return Plugin(SIMPLE_CONTRACT, name="demo", token="t")


async def test_large_operation_requests_are_pulled_before_dispatch():
    broker = Broker()
    broker.input = encode({"operation": "greet", "input": {"who": "x" * 10000}})
    p = plugin()

    async def greet(ctx, raw, out):
        out.vars({"text": str(len(raw["who"]))})

    p.register("greet", greet)
    envelope = {"__sokel_payload": {"version": 1, "subject": "sokel.payload.group.input", "size": len(broker.input), "sha256": hashlib.sha256(broker.input).hexdigest(), "chunk_bytes": 1024}}
    msg = SimpleNamespace(reply="_INBOX.test", subject="sokel.op.group.large", data=encode(envelope))
    await NatsTransport()._dispatch(p, broker, msg, NatsFiles(broker, "t"), "replica")
    assert broker.published == [{"text": "10000"}]
    assert len(broker.fetched) == (len(broker.input) + 1023) // 1024


async def test_older_platform_receives_typed_oversized_reply_error():
    broker = Broker()
    p = plugin()

    async def greet(ctx, raw, out):
        out.vars({"text": raw["who"]})

    p.register("greet", greet)
    msg = SimpleNamespace(reply="_INBOX.test", subject="sokel.op.group", data=encode({"operation": "greet", "input": {"who": "x" * 10000}}))
    await NatsTransport()._dispatch(p, broker, msg, NatsFiles(broker, "t"), "replica")
    assert len(broker.published) == 1
    assert broker.published[0]["code"] == "payload_too_large"


async def test_large_replies_transfer_intact_with_header_and_cleanup():
    broker = Broker()
    broker.max_payload = 64 * 1024
    p = plugin()
    text = "雪" * (3 * 1024 * 1024)  # json.dumps escapes this to 18 MiB.

    async def greet(ctx, raw, out):
        out.vars({"text": text})

    p.register("greet", greet)
    msg = SimpleNamespace(reply="_INBOX.test", subject="sokel.op.group", data=encode({"operation": "greet", "payload_protocol": 1, "deadline_ms": 5000}))
    await NatsTransport()._dispatch(p, broker, msg, NatsFiles(broker, "t"), "replica")
    await asyncio.gather(*broker.consumers)
    assert broker.received == [{"text": text}]
    assert broker.wire_headers[0] == {"Sokel-Instance": "replica", "Sokel-Payload": "1"}
    assert not broker.subscriptions


async def test_stream_frames_are_transferred_in_order_before_end():
    broker = Broker()
    p = plugin()

    async def stream(ctx, raw, out):
        out.text("x" * 10000)
        out.text("next")
        out.vars({"n": 3})

    p.register("stream_it", stream)
    msg = SimpleNamespace(reply="_INBOX.test", subject="sokel.op.group", data=encode({"operation": "stream_it", "payload_protocol": 1, "deadline_ms": 1000}))
    await NatsTransport()._dispatch(p, broker, msg, NatsFiles(broker, "t"), "replica")
    await asyncio.gather(*broker.consumers)
    assert broker.received == [{"kind": "text", "text": "x" * 10000}, {"kind": "text", "text": "next"}, {"kind": "variables", "vars": {"n": 3}}, {"kind": "end"}]
    assert not broker.subscriptions


async def test_business_reference_like_output_is_not_a_transport_envelope():
    broker = Broker()
    value = {"__sokel_payload": {"version": 1, "subject": "business.data"}}
    await publish_payload(broker, "group", "_INBOX.test", encode(value), True)
    assert broker.received == [value]
    assert not broker.wire_headers[0]


@pytest.mark.parametrize("change", [
    {"subject": "sokel.payload.other.input"}, {"subject": "sokel.payload.group.*"},
    {"subject": "sokel.payload.group.extra.token"}, {"version": 2}, {"version": True},
    {"size": -1}, {"size": 512 * 1024 * 1024 + 1}, {"chunk_bytes": 0},
    {"chunk_bytes": 1024 * 1024 + 1}, {"sha256": "f"},
])
async def test_invalid_references_are_rejected_without_requests(change):
    broker = Broker()
    ref = {"version": 1, "subject": "sokel.payload.group.input", "size": 1, "sha256": hashlib.sha256(b"x").hexdigest(), "chunk_bytes": 1}
    ref.update(change)
    with pytest.raises(PayloadError, match="invalid payload reference"):
        await fetch_payload(broker, "group", encode({"__sokel_payload": ref}))
    assert not broker.fetched
    assert not broker.cancelled


@pytest.mark.parametrize("size", [3, 4])
async def test_corrupt_or_truncated_chunks_cancel_the_producer(size):
    broker = Broker()
    broker.input = b"bad"
    ref = {"version": 1, "subject": "sokel.payload.group.input", "size": size, "sha256": hashlib.sha256(b"good").hexdigest(), "chunk_bytes": 1024}
    with pytest.raises(PayloadError, match="mismatch"):
        await fetch_payload(broker, "group", encode({"__sokel_payload": ref}))
    assert broker.cancelled


async def test_abandoned_transfer_expires_and_unsubscribes():
    broker = Broker()

    async def ignore_reply(*args, **kwargs):
        pass

    broker.publish = ignore_reply
    with pytest.raises(PayloadError, match="deadline"):
        await publish_payload(broker, "group", "_INBOX.test", b"x" * 10000, True, deadline=time.monotonic() + 0.02)
    assert not broker.subscriptions


async def test_stream_values_are_snapshotted_when_emitted():
    broker = Broker()
    p = plugin()

    async def stream(ctx, raw, out):
        value = {"n": 1}
        out.json(value)
        value["n"] = 2

    p.register("stream_it", stream)
    msg = SimpleNamespace(reply="_INBOX.test", subject="sokel.op.group", data=encode({"operation": "stream_it", "payload_protocol": 1}))
    await NatsTransport()._dispatch(p, broker, msg, NatsFiles(broker, "t"), "replica")
    assert broker.received == [{"kind": "json", "json": {"n": 1}}, {"kind": "end"}]


async def test_expired_large_call_does_not_invoke_handler():
    broker = Broker()
    broker.input = encode({"operation": "greet", "deadline_ms": 1, "input": {"who": "x" * 10000}})
    original_request = broker.request

    async def slow_request(*args, **kwargs):
        await asyncio.sleep(0.002)
        return await original_request(*args, **kwargs)

    broker.request = slow_request
    invoked = False
    p = plugin()

    async def greet(ctx, raw, out):
        nonlocal invoked
        invoked = True

    p.register("greet", greet)
    ref = {"__sokel_payload": {"version": 1, "subject": "sokel.payload.group.input", "size": len(broker.input), "sha256": hashlib.sha256(broker.input).hexdigest(), "chunk_bytes": 1024}}
    msg = SimpleNamespace(reply="_INBOX.test", subject="sokel.op.group.large", data=encode(ref))
    await NatsTransport()._dispatch(p, broker, msg, NatsFiles(broker, "t"), "replica")
    assert not invoked
    assert broker.received[0]["code"] == "retryable"


@pytest.mark.parametrize("chunk_request", [{"cancel": True}, {"offset": 1}])
async def test_cancel_or_invalid_offset_immediately_releases_producer(chunk_request):
    broker = Broker()

    async def trigger_request(subject, data, **kwargs):
        ref = json.loads(data)["__sokel_payload"]

        async def respond(chunk):
            pass

        await broker.subscriptions[ref["subject"]](SimpleNamespace(data=encode(chunk_request), respond=respond))

    broker.publish = trigger_request
    with pytest.raises(PayloadError, match="cancelled|offset"):
        await publish_payload(broker, "group", "_INBOX.test", b"x" * 10000, True)
    assert not broker.subscriptions
