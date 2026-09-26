/** The platform's deadline_ms aborts ctx.signal and fails the call as retryable (F-327). */

import assert from "node:assert/strict";
import { test } from "node:test";

import { Plugin, Retryable } from "../src/index.js";
import { contract } from "./helpers.js";

test("a call past deadline_ms fails as retryable and aborts ctx.signal", async () => {
  const p = new Plugin({ contract: contract(), name: "demo", token: "t" });
  let aborted = false;
  const op = "greet";
  p.register(op, async (ctx) => {
    ctx.signal.addEventListener("abort", () => { aborted = true; });
    await new Promise((r) => setTimeout(r, 2000));
  });
  await assert.rejects(p.dispatchBuffered({ operation: op, input: {}, deadline_ms: 50 }), Retryable);
  assert.ok(aborted, "ctx.signal must abort at the deadline");
});

test("no deadline_ms: the signal never aborts", async () => {
  const p = new Plugin({ contract: contract(), name: "demo", token: "t" });
  const op = "greet";
  p.register(op, async (ctx, _in, out) => { out.vars({ aborted: ctx.signal.aborted }); });
  const vars = await p.dispatchBuffered({ operation: op, input: {} });
  assert.equal(vars.aborted, false);
});
