/** File chunking: bytes do not travel on an operation reply, which max_payload bounds, but on a
 * dedicated channel (§6 of the protocol). */

import assert from "node:assert/strict";
import { test } from "node:test";

import { NatsFiles } from "../src/nats.js";

type Req = Record<string, any>;

class FakeNC {
  readonly sent: Array<[string, Req]> = [];
  constructor(private readonly reply: (subject: string, req: Req) => unknown) {}
  async request(subject: string, data: Uint8Array): Promise<{ data: Uint8Array }> {
    const req = JSON.parse(new TextDecoder().decode(data));
    this.sent.push([subject, req]);
    return { data: new TextEncoder().encode(JSON.stringify(this.reply(subject, req))) };
  }
}

test("fetching a file: pull chunk by chunk until last", async () => {
  const payload = Buffer.from("0123456789");
  const nc = new FakeNC((_s, req) => ({
    data: (req.seq === 0 ? payload.subarray(0, 5) : payload.subarray(5)).toString("base64"),
    last: req.seq === 1,
  }));
  const got = await new NatsFiles(nc as never, "t").fetch({ id: "f_1" });
  assert.equal(Buffer.from(got).toString(), "0123456789");
});

test("an older reference with only a url takes its last segment as the id", async () => {
  const nc = new FakeNC(() => ({ data: "", last: true }));
  await new NatsFiles(nc as never, "t").fetch({ url: "https://host/files/f_9" });
  assert.equal(nc.sent[0][1].id, "f_9");
});

test("storing a file: chunked, continuing an upload_id, with the reference on the last chunk", async () => {
  const data = new Uint8Array(1024 * 1024 + 7); // just over one chunk, so it takes two rounds
  const nc = new FakeNC((_s, req) =>
    req.last ? { upload_id: "up_1", file: { id: "f_2", name: req.name } } : { upload_id: "up_1" },
  );
  const f = await new NatsFiles(nc as never, "t").store("big.bin", "application/octet-stream", data);
  assert.deepEqual(nc.sent.map(([, r]) => r.seq), [0, 1]);
  assert.deepEqual(nc.sent.map(([, r]) => r.last), [false, true]);
  // The first chunk sends no upload_id and the platform returns one to continue with; getting that wrong
  // opens a session per chunk and the file never reassembles
  assert.equal(nc.sent[0][1].upload_id, "");
  assert.equal(nc.sent[1][1].upload_id, "up_1");
  assert.equal(f.id, "f_2");
});

test("an empty file still takes one round, or the platform has no session to close", async () => {
  const nc = new FakeNC((_s, req) => (req.last ? { upload_id: "up", file: { id: "f_0" } } : { upload_id: "up" }));
  const f = await new NatsFiles(nc as never, "t").store("empty.txt", "text/plain", new Uint8Array(0));
  assert.equal(nc.sent.length, 1);
  assert.equal(nc.sent[0][1].last, true);
  assert.equal(f.id, "f_0");
});

test("streaming while reading: small upstream pieces accumulate into whole chunks before sending", async () => {
  // An fs stream yields 64 KB at a time by default; sending those as-is would multiply the chunk count
  // more than tenfold, and every chunk is one request-reply
  const nc = new FakeNC((_s, req) =>
    req.last ? { upload_id: "up", file: { id: "f_s" } } : { upload_id: "up" },
  );
  async function* src() {
    for (let i = 0; i < 40; i++) yield new Uint8Array(64 * 1024); // 40 × 64KB = 2.5 MiB
  }
  const f = await new NatsFiles(nc as never, "t").storeStream("big.mp4", "video/mp4", src());
  assert.deepEqual(nc.sent.map(([, r]) => r.seq), [0, 1, 2]);
  assert.deepEqual(nc.sent.map(([, r]) => r.last), [false, false, true]);
  assert.equal(f.id, "f_s");
});
