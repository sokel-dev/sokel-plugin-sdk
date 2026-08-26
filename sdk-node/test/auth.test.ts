/** 协作式认证：步骤由声明定死，实现只挂在保留操作 id 上。 */

import assert from "node:assert/strict";
import { test } from "node:test";

import { Plugin } from "../src/index.js";
import type { ContractData } from "../src/index.js";

function base(): ContractData {
  return {
    name: "demo",
    operations: [{ id: "noop", inputs: [], outputs: [] }],
    auth_flow: { kind: "input", steps: ["start", "poll", "submit"] },
  };
}

test("start / poll / submit 的形状", async () => {
  const p = new Plugin({ contract: base(), name: "demo", token: "t" });
  const submitted = new Map<string, string>();
  p.registerAuth({
    start: () => ({ authId: "a1", prompt: "填验证码", expiresIn: 60 }),
    poll: (_ctx, id) =>
      submitted.has(id) ? { status: "confirmed", session: { k: "v" } } : { status: "pending" },
    submit: (_ctx, id, value) => {
      submitted.set(id, value);
    },
  });

  const start = await p.dispatchBuffered({ operation: "auth.start", input: {} });
  assert.equal(start.auth_id, "a1");
  assert.deepEqual(start.challenge, { kind: "input", qr_image: "", prompt: "填验证码" });
  assert.equal(start.expires_in, 60);

  // pending 时不能带 session：带了等于让平台反复覆写凭证行
  assert.deepEqual(await p.dispatchBuffered({ operation: "auth.poll", input: { auth_id: "a1" } }), {
    status: "pending",
  });

  assert.deepEqual(
    await p.dispatchBuffered({ operation: "auth.submit", input: { auth_id: "a1", input: "123456" } }),
    { ok: true },
  );
  assert.deepEqual(await p.dispatchBuffered({ operation: "auth.poll", input: { auth_id: "a1" } }), {
    status: "confirmed",
    session: { k: "v" },
  });
});

test("实现多于声明当场被拦", () => {
  // qr 只有 start+poll；多写一个 submit = 一份永远不会被调用的实现
  const data = base();
  data.auth_flow = { kind: "qr", steps: ["start", "poll"] };
  const p = new Plugin({ contract: data, name: "demo", token: "t" });
  assert.throws(
    () =>
      p.registerAuth({
        start: () => ({ prompt: "x" }),
        poll: () => ({ status: "pending" }),
        submit: () => {},
      }),
    /没有 "submit" 这一步/,
  );
});

test("认证流的保留 id 不参与单操作兜底", async () => {
  const p = new Plugin({ contract: base(), name: "demo", token: "t" });
  p.registerAuth({ start: () => ({ prompt: "x" }), poll: () => ({ status: "pending" }) });
  p.register("noop", async (_ctx, _raw, out) => out.vars({ ok: true }));
  assert.deepEqual(await p.dispatchBuffered({ operation: "", input: {} }), { ok: true });
});
