/**
 * Webhooks received on the plugin's behalf: decoding the frame, replying, and counting events.
 *
 * The event count is the platform panel's only basis for answering "the request arrived, so why did no
 * workflow start", so it has to count **events successfully pushed**, not how many times the handler ran.
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

test("a webhook pushes events and counts them", async () => {
  const p = new Plugin({ contract: contract(), name: "demo", token: "t" });
  p.registerWebhook(async (ctx, req: WebhookRequest) => {
    assert.equal(req.header("x-sokel-token"), "secret"); // header lookup is case-insensitive
    await ctx.trigger("ping", "e1", { at: "now" });
    return ok();
  });
  const resp = await p.handleWebhook(sctx(), frame({ a: 1 }, { "X-Sokel-Token": "secret" }));
  assert.equal(resp.status, 200);
  assert.equal(resp.events, 1);
});

test("a failed signature check can refuse outright, pushing no events", async () => {
  const p = new Plugin({ contract: contract(), name: "demo", token: "t" });
  p.registerWebhook(() => text(401, "bad token"));
  const resp = await p.handleWebhook(sctx(), frame({}));
  assert.equal(resp.status, 401);
  assert.equal(Buffer.from(resp.body_b64 as string, "base64").toString(), "bad token");
  assert.equal(resp.events, 0);
});

test("the body is raw bytes", async () => {
  // Signature checks are byte-exact: re-encoding as JSON would flatten the extra space and the signature
  // would no longer match
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

test("with no handler registered it fails readably", async () => {
  const p = new Plugin({ contract: contract(), name: "demo", token: "t" });
  const resp = await p.handleWebhook(sctx(), frame({}));
  assert.equal(resp.status, 0);
  assert.match(String(resp.error), /no webhook handler/);
});
