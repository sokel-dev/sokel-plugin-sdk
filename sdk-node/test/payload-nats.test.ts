import assert from "node:assert/strict";
import { createHash } from "node:crypto";
import { test } from "node:test";
import { connect, createInbox } from "nats";
import { Plugin } from "../src/index.js";
import { NatsFiles, NatsTransport } from "../src/nats.js";
import { fetchPayload } from "../src/payload.js";
import { contract } from "./helpers.js";

const server = process.env.SOKEL_TEST_NATS_URL;
const encode = (value: unknown) => Buffer.from(JSON.stringify(value));

// The Go integration fixture starts an isolated broker. This optional companion exercises the
// actual JS NATS client rather than a mock; no production credentials or data are needed.
test("real NATS transports large request, reply and ordered stream frames", { skip: !server, timeout: 10000 }, async () => {
  const nc = await connect({ servers: server });
  const caller = await connect({ servers: server });
  const group = "node_test";
  const subject = `sokel.plugin.demo.${group}`;
  const p = new Plugin({ contract: contract(), name: "demo", token: "t" });
  const text = "雪".repeat(50000);
  p.register("greet", async (_ctx, raw, out) => out.vars({ text: raw.who }));
  p.register("stream_it", async (_ctx, _raw, out) => { out.text(text); out.text("next"); });
  const transport = new NatsTransport() as any;
  const jobs: Promise<void>[] = [];
  const callback = (_err: unknown, msg: unknown) => { jobs.push(transport.dispatch(p, nc, msg, new NatsFiles(nc, "t"), "test-instance")); };
  nc.subscribe(subject, { callback });
  nc.subscribe(`${subject}.large`, { callback });
  await nc.flush();
  try {
    const call = encode({ operation: "greet", input: { who: text }, payload_protocol: 1, deadline_ms: 5000 });
    const chunkBytes = Math.min(1024 * 1024, caller.info!.max_payload - 1024);
    const source = caller.subscribe(`sokel.payload.${group}.input`, { callback: (_err, msg) => {
      const { offset } = JSON.parse(Buffer.from(msg.data).toString());
      msg.respond(call.subarray(offset, offset + chunkBytes));
    } });
    await caller.flush();
    const response = await caller.request(`${subject}.large`, encode({ __sokel_payload: {
      version: 1, subject: source.getSubject(), size: call.length, sha256: createHash("sha256").update(call).digest("hex"), chunk_bytes: chunkBytes,
    } }), { timeout: 5000 });
    const data = response.headers?.get("Sokel-Payload") === "1" ? await fetchPayload(caller, group, response.data) : response.data;
    assert.deepEqual(JSON.parse(Buffer.from(data).toString()), { text });
    assert.equal(response.headers?.get("Sokel-Instance"), "test-instance");
    source.unsubscribe();

    const inbox = caller.subscribe(createInbox());
    await caller.flush();
    caller.publish(subject, encode({ operation: "stream_it", payload_protocol: 1, deadline_ms: 5000 }), { reply: inbox.getSubject() });
    const frames = [];
    for await (const msg of inbox) {
      const body = msg.headers?.get("Sokel-Payload") === "1" ? await fetchPayload(caller, group, msg.data) : msg.data;
      const frame = JSON.parse(Buffer.from(body).toString());
      frames.push(frame);
      if (frame.kind === "end") break;
    }
    inbox.unsubscribe();
    assert.deepEqual(frames, [{ kind: "text", text }, { kind: "text", text: "next" }, { kind: "end" }]);
    await Promise.all(jobs);
  } finally { await caller.close(); await nc.close(); }
});
