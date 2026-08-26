/**
 * 平台代收 webhook：帧的解与应答，以及 events 计数。
 *
 * events 计数是平台面板回答「请求到了但为什么没起工作流」的唯一依据，
 * 所以它必须数**成功推出去的**事件，而不是 handler 被调了几次。
 */

import assert from "node:assert/strict";
import { test } from "node:test";

import { Plugin, SourceCtx, ok, text } from "../src/index.js";
import type { WebhookRequest } from "../src/index.js";
import { contract } from "./helpers.js";

function frame(body: unknown, headers: Record<string, string> = {}) {
  return { method: "POST", headers, body_b64: Buffer.from(JSON.stringify(body)).toString("base64") };
}

function sctx(): SourceCtx {
  return new SourceCtx({
    token: "t",
    publish: () => {},
    validEvents: ["ping"],
    credential: { api_key: "secret" },
  });
}

test("webhook 推事件并计数", async () => {
  const p = new Plugin({ contract: contract(), name: "demo", token: "t" });
  p.registerWebhook(async (ctx, req: WebhookRequest) => {
    assert.equal(req.header("x-sokel-token"), "secret"); // 取头大小写不敏感
    await ctx.trigger("ping", "e1", { at: "now" });
    return ok();
  });
  const resp = await p.handleWebhook(sctx(), frame({ a: 1 }, { "X-Sokel-Token": "secret" }));
  assert.equal(resp.status, 200);
  assert.equal(resp.events, 1);
});

test("验签失败可以直接拒掉，不推任何事件", async () => {
  const p = new Plugin({ contract: contract(), name: "demo", token: "t" });
  p.registerWebhook(() => text(401, "bad token"));
  const resp = await p.handleWebhook(sctx(), frame({}));
  assert.equal(resp.status, 401);
  assert.equal(Buffer.from(resp.body_b64 as string, "base64").toString(), "bad token");
  assert.equal(resp.events, 0);
});

test("body 是原始字节", async () => {
  // 验签要逐字节一致：JSON 重编码会抹平多余空格，签名就对不上了
  const p = new Plugin({ contract: contract(), name: "demo", token: "t" });
  let raw = "";
  p.registerWebhook((_ctx, req) => {
    raw = req.body.toString();
    return ok();
  });
  const original = '{"a":  1}';
  await p.handleWebhook(sctx(), { body_b64: Buffer.from(original).toString("base64") });
  assert.equal(raw, original);
});

test("没注册处理器时给出可读的失败", async () => {
  const p = new Plugin({ contract: contract(), name: "demo", token: "t" });
  const resp = await p.handleWebhook(sctx(), frame({}));
  assert.equal(resp.status, 0);
  assert.match(String(resp.error), /no webhook handler/);
});
