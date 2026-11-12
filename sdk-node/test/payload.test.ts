import assert from "node:assert/strict";
import { createHash } from "node:crypto";
import { test } from "node:test";
import { Plugin } from "../src/index.js";
import { NatsFiles, NatsTransport } from "../src/nats.js";
import { contract } from "./helpers.js";
import { fetchPayload, publishPayload } from "../src/payload.js";

const encode = (value: unknown) => Buffer.from(JSON.stringify(value));
const digest = (data: Uint8Array) => createHash("sha256").update(data).digest("hex");

class Broker {
  info = { max_payload: 4096 };
  published: any[] = [];
  fetched: number[] = [];
  input = Buffer.alloc(0);
  subscriptions = new Map<string, any>();
  received: any[] = [];
  wireHeaders: any[] = [];
  consumers: Promise<void>[] = [];
  cancelled = false;
  async request(_subject: string, data: Uint8Array): Promise<any> {
    const request = JSON.parse(Buffer.from(data).toString());
    if (request.cancel) { this.cancelled = true; return { data: Buffer.alloc(0) }; }
    if (this.subscriptions.has(_subject)) return new Promise((resolve, reject) => {
      this.subscriptions.get(_subject)(null, { data, respond: (chunk: Uint8Array) => { resolve({ data: chunk }); return true; } });
    });
    const { offset } = request;
    this.fetched.push(offset);
    return { data: this.input.subarray(offset, offset + 1024) };
  }
  publish(_subject: string, data: Uint8Array, options?: any): void {
    if (data.length > this.info.max_payload) throw new Error("MAX_PAYLOAD_EXCEEDED");
    const value = JSON.parse(Buffer.from(data).toString());
    this.published.push(value);
    this.wireHeaders.push(options?.headers);
    if (options?.headers?.get("Sokel-Payload") === "1") {
      this.consumers.push((async () => {
        const r = value.__sokel_payload;
        const chunks: Uint8Array[] = [];
        for (let offset = 0; offset < r.size;) {
          const response = await this.request(r.subject, encode({ offset }));
          assert.equal(response.data.length, Math.min(r.chunk_bytes, r.size - offset));
          chunks.push(response.data);
          offset += response.data.length;
        }
        const body = Buffer.concat(chunks);
        assert.equal(digest(body), r.sha256);
        this.received.push(JSON.parse(body.toString()));
      })());
    } else this.received.push(value);
  }
  subscribe(subject: string, options: any): any {
    this.subscriptions.set(subject, options.callback);
    return { unsubscribe: () => this.subscriptions.delete(subject) };
  }
  async flush(): Promise<void> {}
}

async function dispatch(broker: Broker, call: unknown, large = false): Promise<void> {
  const p = new Plugin({ contract: contract(), name: "demo", token: "t" });
  p.register("greet", async (_ctx, input, out) => out.vars({ text: input.who }));
  await (new NatsTransport() as any).dispatch(p, broker, {
    reply: "_INBOX.test", subject: `sokel.op.group${large ? ".large" : ""}`, data: encode(call),
  }, new NatsFiles(broker as any, "t"), "replica");
}

test("large operation requests are pulled before dispatch without truncation", async () => {
  const broker = new Broker();
  broker.input = encode({ operation: "greet", input: { who: "x".repeat(10000) } });
  // Keep the response small so the test isolates request transfer.
  const p = new Plugin({ contract: contract(), name: "demo", token: "t" });
  p.register("greet", async (_ctx, input, out) => out.vars({ text: String(String(input.who).length) }));
  const data = encode({ __sokel_payload: { version: 1, subject: "sokel.payload.group.input", size: broker.input.length, sha256: digest(broker.input), chunk_bytes: 1024 } });
  await (new NatsTransport() as any).dispatch(p, broker, { reply: "_INBOX.test", subject: "sokel.op.group.large", data }, new NatsFiles(broker as any, "t"), "replica");
  assert.deepEqual(broker.published, [{ text: "10000" }]);
  assert.equal(broker.fetched.length, Math.ceil(broker.input.length / 1024));
});

test("an older platform receives a typed error for an oversized reply", async () => {
  const broker = new Broker();
  await dispatch(broker, { operation: "greet", input: { who: "x".repeat(10000) } });
  assert.equal(broker.published.length, 1);
  assert.equal(broker.published[0].code, "payload_too_large");
});

test("large replies transfer intact, carry an explicit header, and release producers", async () => {
  const broker = new Broker();
  broker.info.max_payload = 64 * 1024;
  const text = "雪".repeat(6 * 1024 * 1024); // 18 MiB UTF-8, above the production limit.
  await dispatch(broker, { operation: "greet", input: { who: text }, payload_protocol: 1, deadline_ms: 5000 });
  await Promise.all(broker.consumers);
  assert.deepEqual(broker.received, [{ text }]);
  assert.equal(broker.wireHeaders[0].get("Sokel-Payload"), "1");
  assert.equal(broker.wireHeaders[0].get("Sokel-Instance"), "replica");
  assert.equal(broker.subscriptions.size, 0);
});

test("stream frames finish transferring before subsequent frames and end", async () => {
  const broker = new Broker();
  const p = new Plugin({ contract: contract(), name: "demo", token: "t" });
  p.register("stream_it", async (_ctx, _input, out) => { out.text("x".repeat(10000)); out.text("next"); out.vars({ n: 3 }); });
  await (new NatsTransport() as any).dispatch(p, broker, { reply: "_INBOX.test", subject: "sokel.op.group", data: encode({ operation: "stream_it", payload_protocol: 1, deadline_ms: 1000 }) }, new NatsFiles(broker as any, "t"), "replica");
  await Promise.all(broker.consumers);
  assert.deepEqual(broker.received, [{ kind: "text", text: "x".repeat(10000) }, { kind: "text", text: "next" }, { kind: "variables", vars: { n: 3 } }, { kind: "end" }]);
  assert.equal(broker.subscriptions.size, 0);
});

test("ordinary outputs resembling references are not transport envelopes", async () => {
  const broker = new Broker();
  const value = { __sokel_payload: { version: 1, subject: "business.data" } };
  await publishPayload(broker as any, "group", "_INBOX.test", encode(value), true);
  assert.deepEqual(broker.received, [value]);
  assert.equal(broker.wireHeaders[0]?.get("Sokel-Payload"), undefined);
});

test("invalid and cross-group references are rejected before any request", async () => {
  const valid = { version: 1, subject: "sokel.payload.group.input", size: 1, sha256: digest(Buffer.from("x")), chunk_bytes: 1 };
  for (const change of [{ subject: "sokel.payload.other.input" }, { subject: "sokel.payload.group.*" }, { subject: "sokel.payload.group.extra.token" }, { version: 2 }, { size: -1 }, { size: 512 * 1024 * 1024 + 1 }, { chunk_bytes: 0 }, { chunk_bytes: 1024 * 1024 + 1 }, { sha256: "f" }]) {
    const broker = new Broker();
    await assert.rejects(fetchPayload(broker as any, "group", encode({ __sokel_payload: { ...valid, ...change } })), /invalid payload reference/);
    assert.deepEqual(broker.fetched, []);
    assert.equal(broker.cancelled, false);
  }
});

test("corrupt and truncated chunks fail and cancel their producer", async () => {
  for (const size of [3, 4]) {
    const broker = new Broker();
    broker.input = Buffer.from("bad");
    await assert.rejects(fetchPayload(broker as any, "group", encode({ __sokel_payload: { version: 1, subject: "sokel.payload.group.input", size, sha256: digest(Buffer.from("good")), chunk_bytes: 1024 } })), /mismatch/);
    assert.equal(broker.cancelled, true);
  }
});

test("an abandoned transfer expires and removes its subscription", async () => {
  const broker = new Broker();
  broker.publish = () => {}; // The receiver disappears after getting the reference.
  await assert.rejects(publishPayload(broker as any, "group", "_INBOX.test", Buffer.alloc(10000), true, undefined, Date.now() + 20), /deadline/);
  assert.equal(broker.subscriptions.size, 0);
});

test("stream values are snapshotted when emitted", async () => {
  const broker = new Broker();
  const p = new Plugin({ contract: contract(), name: "demo", token: "t" });
  p.register("stream_it", async (_ctx, _input, out) => {
    const value = { n: 1 };
    out.json(value);
    value.n = 2;
  });
  await (new NatsTransport() as any).dispatch(p, broker, { reply: "_INBOX.test", subject: "sokel.op.group", data: encode({ operation: "stream_it", payload_protocol: 1 }) }, new NatsFiles(broker as any, "t"), "replica");
  assert.deepEqual(broker.received, [{ kind: "json", json: { n: 1 } }, { kind: "end" }]);
});

test("expired large calls do not invoke the handler", async () => {
  const broker = new Broker();
  broker.input = encode({ operation: "greet", deadline_ms: 1, input: { who: "x".repeat(10000) } });
  const request = broker.request.bind(broker);
  broker.request = async (...args) => { await new Promise((resolve) => setTimeout(resolve, 2)); return request(...args); };
  let invoked = false;
  const p = new Plugin({ contract: contract(), name: "demo", token: "t" });
  p.register("greet", async () => { invoked = true; });
  const data = encode({ __sokel_payload: { version: 1, subject: "sokel.payload.group.input", size: broker.input.length, sha256: digest(broker.input), chunk_bytes: 1024 } });
  await (new NatsTransport() as any).dispatch(p, broker, { reply: "_INBOX.test", subject: "sokel.op.group.large", data }, new NatsFiles(broker as any, "t"), "replica");
  assert.equal(invoked, false);
  assert.equal(broker.received[0].code, "retryable");
});

test("cancel and out-of-order offsets release the producer immediately", async () => {
  for (const request of [{ cancel: true }, { offset: 1 }]) {
    const broker = new Broker();
    broker.publish = (_subject, data) => {
      const ref = JSON.parse(Buffer.from(data).toString()).__sokel_payload;
      broker.subscriptions.get(ref.subject)(null, { data: encode(request), respond: () => true });
    };
    await assert.rejects(publishPayload(broker as any, "group", "_INBOX.test", Buffer.alloc(10000), true), /cancelled|offset/);
    assert.equal(broker.subscriptions.size, 0);
  }
});
