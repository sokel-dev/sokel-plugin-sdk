// Copyright 2026 The Sokel Authors
// SPDX-License-Identifier: Apache-2.0

/**
 * 一致性套件（Node 侧）：本实现上报的契约必须等于 golden。
 *
 * 目标不是「保证同一个插件的多语言实现一致」（那种需求很少），而是
 * **保证各 SDK 对协议的理解一致**——一个参考插件就够，不必每个插件都跨语言。
 * Python 侧有一条同样的断言（sdk-python/tests/test_register_payload.py）。
 */

import assert from "node:assert/strict";
import { readFileSync } from "node:fs";
import { test } from "node:test";

import { CONTRACT } from "../src/sokel.gen.js";

// 路径相对**编译后**的 dist/test/ —— 从那里回到插件根目录要三级
const golden = JSON.parse(readFileSync(new URL("../../../contract.golden.json", import.meta.url), "utf8"));

test("生成物内嵌的契约等于 golden", () => {
  assert.deepEqual(CONTRACT, golden);
});

test("认证流的保留操作在契约里", () => {
  // 平台面板按契约构造 /credentials/{id}/auth/{step} 的请求，契约里没有它就不知道发什么
  const ids = CONTRACT.operations.map((op) => op.id);
  assert.deepEqual(ids.filter((id) => id.startsWith("auth.")), ["auth.start", "auth.poll", "auth.submit"]);
  assert.ok(CONTRACT.operations.filter((op) => op.id.startsWith("auth.")).every((op) => op.internal));
});
