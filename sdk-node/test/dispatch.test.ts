/**
 * 分发：未知操作、非流式合并、流式逐帧。
 *
 * 这段决定平台看到的回复长什么样，而线上最难查的恰恰是「回复形态不对」
 * （节点拿到空对象，日志里一切正常）。
 */

import assert from "node:assert/strict";
import { test } from "node:test";

import { Plugin } from "../src/index.js";
import type { Frame } from "../src/index.js";
import { contract } from "./helpers.js";

function makePlugin(): Plugin {
  return new Plugin({ contract: contract(), name: "demo", token: "skp_test" });
}

test("非流式：合并 variables 帧作为单次回复", async () => {
  const p = makePlugin();
  p.register("greet", async (_ctx, raw, out) => {
    out.vars({ text: `hi ${raw.who}` });
    out.text("这条只在流式里给人看，不进输出");
  });
  assert.deepEqual(await p.dispatchBuffered({ operation: "greet", input: { who: "sokel" } }), {
    text: "hi sokel",
  });
});

test("未知操作是一次可见的失败", async () => {
  const p = makePlugin();
  p.register("greet", async () => {});
  p.register("stream_it", async () => {});
  await assert.rejects(() => p.dispatchBuffered({ operation: "nope", input: {} }), /unknown operation/);
});

test("单操作插件：operation 省略时打到唯一那个", async () => {
  const p = makePlugin();
  p.register("greet", async (_ctx, raw, out) => out.vars({ text: `hi ${raw.who}` }));
  assert.deepEqual(await p.dispatchBuffered({ input: { who: "x" } }), { text: "hi x" });
});

test("流式：逐帧且保序", async () => {
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

test("契约里没有的操作注册不上", async () => {
  // 否则那份实现永远等不到调用，而且毫无症状
  const p = makePlugin();
  assert.throws(() => p.register("ghost", async () => {}), /不在契约里/);
});

test("凭证与追踪上下文到得了 handler", async () => {
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
  // 没有追踪上下文时是空串：调用方要把它当「没有重试语义」，而不是一个恒定的键
  assert.equal(missing, "");
});
