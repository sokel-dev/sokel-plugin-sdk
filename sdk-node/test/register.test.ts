/**
 * 注册握手：声明了却没上报，是这套自报机制最典型的静默失效。
 * 插件侧一切正常、平台侧什么也没发生——所以每一项契约都要有一条断言盯着它。
 */

import assert from "node:assert/strict";
import { test } from "node:test";

import { Plugin, ok } from "../src/index.js";
import { contract } from "./helpers.js";

test("注册载荷带上契约的每一部分", () => {
  const data = contract();
  data.credential_schema = [{ name: "api_key", type: "secret" }];
  data.events_common = [{ name: "at", type: "string" }];
  data.auth_flow = { kind: "qr", steps: ["start", "poll"] };
  data.capabilities = { recency: false };
  data.doc = "# 说明";
  const p = new Plugin({ contract: data, name: "demo", token: "skp_x", version: "1.2.3" });
  const body = p.registerPayload("inst-1", "host-1", "2026-01-01T00:00:00Z") as Record<string, unknown>;

  assert.equal(body.token, "skp_x");
  assert.equal(body.instance_id, "inst-1");
  assert.equal(body.transport, "nats");
  assert.equal(body.version, "1.2.3");
  for (const key of ["operations", "credential_schema", "events", "events_common", "auth_flow", "capabilities", "doc"]) {
    assert.ok(body[key], `契约的 ${key} 没进注册载荷`);
  }
});

test("注册了 webhook 处理器就自报 webhook 能力", () => {
  // 能力位不靠自报靠事实：作者忘声明不该让入口按钮消失
  const p = new Plugin({ contract: contract(), name: "demo", token: "t" });
  assert.equal((p.registerPayload("i", "h", "T") as any).capabilities, undefined);
  p.registerWebhook(() => ok());
  assert.equal((p.registerPayload("i", "h", "T") as any).capabilities.webhook, true);
});

test("事件源的运行态随注册上报", () => {
  const p = new Plugin({ contract: contract(), name: "demo", token: "t" });
  p.registerSource("poller", "轮询", async () => {});
  p.board.set("poller", "cred_1", "auth_required", "session 失效");
  const states = (p.registerPayload("i", "h", "T") as any).source_states;
  assert.equal(states.length, 1);
  assert.equal(states[0].status, "auth_required");
  assert.equal(states[0].credential_id, "cred_1");
});
