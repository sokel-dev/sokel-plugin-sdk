/** 文件分块：字节不走操作 reply（受 max_payload 约束），走专用通道（协议 §6）。 */

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

test("取文件：逐块拉到 last", async () => {
  const payload = Buffer.from("0123456789");
  const nc = new FakeNC((_s, req) => ({
    data: (req.seq === 0 ? payload.subarray(0, 5) : payload.subarray(5)).toString("base64"),
    last: req.seq === 1,
  }));
  const got = await new NatsFiles(nc as never, "t").fetch({ id: "f_1" });
  assert.equal(Buffer.from(got).toString(), "0123456789");
});

test("只有 url 的老引用取末段当 id", async () => {
  const nc = new FakeNC(() => ({ data: "", last: true }));
  await new NatsFiles(nc as never, "t").fetch({ url: "https://host/files/f_9" });
  assert.equal(nc.sent[0][1].id, "f_9");
});

test("存文件：分块、续用 upload_id、末块拿引用", async () => {
  const data = new Uint8Array(1024 * 1024 + 7); // 一块多一点：必须发两趟
  const nc = new FakeNC((_s, req) =>
    req.last ? { upload_id: "up_1", file: { id: "f_2", name: req.name } } : { upload_id: "up_1" },
  );
  const f = await new NatsFiles(nc as never, "t").store("big.bin", "application/octet-stream", data);
  assert.deepEqual(nc.sent.map(([, r]) => r.seq), [0, 1]);
  assert.deepEqual(nc.sent.map(([, r]) => r.last), [false, true]);
  // 首块 upload_id 为空、平台回一个续用——续错就是每块各开一个会话，最后拼不出文件
  assert.equal(nc.sent[0][1].upload_id, "");
  assert.equal(nc.sent[1][1].upload_id, "up_1");
  assert.equal(f.id, "f_2");
});

test("空文件也要走一轮，否则平台侧没有会话可收尾", async () => {
  const nc = new FakeNC((_s, req) => (req.last ? { upload_id: "up", file: { id: "f_0" } } : { upload_id: "up" }));
  const f = await new NatsFiles(nc as never, "t").store("empty.txt", "text/plain", new Uint8Array(0));
  assert.equal(nc.sent.length, 1);
  assert.equal(nc.sent[0][1].last, true);
  assert.equal(f.id, "f_0");
});
