/**
 * Dispatch: an unknown operation, non-streaming merging, and streaming frame by frame.
 *
 * This decides what the platform sees as a reply, and the hardest production fault to track down is
 * exactly "the reply has the wrong shape" — the node receives an empty object while the logs look fine.
 */

import assert from "node:assert/strict";
import { test } from "node:test";

import { Plugin } from "../src/index.js";
import type { Frame } from "../src/index.js";
import { contract } from "./helpers.js";

function makePlugin(): Plugin {
  return new Plugin({ contract: contract(), name: "demo", token: "skp_test" });
}

test("non-streaming: variable frames merge into one reply", async () => {
  const p = makePlugin();
  p.register("greet", async (_ctx, raw, out) => {
    out.vars({ text: `hi ${raw.who}` });
    out.text("this is for a human watching the stream and never becomes an output");
  });
  assert.deepEqual(await p.dispatchBuffered({ operation: "greet", input: { who: "sokel" } }), {
    text: "hi sokel",
  });
});

test("an unknown operation fails visibly", async () => {
  const p = makePlugin();
  p.register("greet", async () => {});
  p.register("stream_it", async () => {});
  await assert.rejects(() => p.dispatchBuffered({ operation: "nope", input: {} }), /unknown operation/);
});

test("a single-operation plugin: omitting operation hits the only one", async () => {
  const p = makePlugin();
  p.register("greet", async (_ctx, raw, out) => out.vars({ text: `hi ${raw.who}` }));
  assert.deepEqual(await p.dispatchBuffered({ input: { who: "x" } }), { text: "hi x" });
});

test("streaming: frame by frame, in order", async () => {
  const p = makePlugin();
  p.register("stream_it", async (_ctx, _raw, out) => {
    out.text("a");
    out.json({ k: 1 });
    out.vars({ n: 2 });
  });
  const frames: Frame[] = [];
  await p.dispatch({ operation: "stream_it", input: {} }, (f) => frames.push(f));
  assert.deepEqual(
    frames.map((f) => f.kind),
    ["text", "json", "variables"],
  );
  assert.deepEqual(frames[2].vars, { n: 2 });
});

test("an operation absent from the contract cannot be registered", async () => {
  // otherwise the implementation waits forever for a call, with no symptom at all
  const p = makePlugin();
  assert.throws(() => p.register("ghost", async () => {}), /not in the contract/);
});

test("the credential and trace context reach the handler", async () => {
  const p = makePlugin();
  let cred = "";
  let run = "";
  let missing = "-";
  p.register("greet", async (ctx, _raw, out) => {
    cred = ctx.credential.api_key ?? "";
    run = ctx.trace("run_id");
    missing = ctx.trace("node_id");
    out.vars({ text: "ok" });
  });
  await p.dispatchBuffered({
    operation: "greet",
    input: { who: "x" },
    credential: { api_key: "k" },
    trace: { run_id: "run_1" },
  });
  assert.equal(cred, "k");
  assert.equal(run, "run_1");
  // With no trace context it is an empty string, which callers must read as "no retry semantics" rather
  // than as a constant key
  assert.equal(missing, "");
});
