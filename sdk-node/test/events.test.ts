/** 事件源：声明校验、payload 形态、per-credential reconcile。 */

import assert from "node:assert/strict";
import { test } from "node:test";

import { CredEntry, Plugin, SourceCtx, SourceSupervisor, StateBoard, desiredSourceCreds } from "../src/index.js";
import { contract } from "./helpers.js";

function makeCtx(sent: Array<[string, string]>, opts: Partial<ConstructorParameters<typeof SourceCtx>[0]> = {}) {
  return new SourceCtx({
    token: "skp_x",
    publish: (subject, data) => {
      sent.push([subject, new TextDecoder().decode(data)]);
    },
    validEvents: ["ping"],
    ...opts,
  });
}

test("trigger 的消息形状", async () => {
  const sent: Array<[string, string]> = [];
  await makeCtx(sent, { credentialId: "cred_1" }).trigger("ping", "evt-1", { at: "now" });
  const [subject, data] = sent[0];
  assert.equal(subject, "sokel.trigger");
  assert.deepEqual(JSON.parse(data), {
    token: "skp_x",
    event: "ping",
    payload: { at: "now" },
    event_id: "evt-1",
    credential_id: "cred_1", // 路由键由 SDK 自动回带，源代码不用管
  });
});

test("拼错的事件名当场报错", async () => {
  // 否则它变成一条平台侧无人认领的消息，没有任何症状
  await assert.rejects(() => makeCtx([]).trigger("pong", "1", {}), /undeclared event/);
});

test("回写凭证要有绑定的凭证", async () => {
  await assert.rejects(() => makeCtx([]).updateCredential({ session: "x" }), /no bound credential/);
});

test("回写凭证会同时更新本地副本", async () => {
  const sent: Array<[string, string]> = [];
  const ctx = makeCtx(sent, { credentialId: "cred_1", credential: { session: "old" } });
  await ctx.updateCredential({ session: "new" });
  assert.equal(sent[0][0], "sokel.credential.update");
  assert.deepEqual(JSON.parse(sent[0][1]).patch, { session: "new" });
  // 本地也要跟上，否则下一拍还用旧值
  assert.equal(ctx.credential.session, "new");
});

test("supervisor 按凭证起停，字段变了就重启", () => {
  const started: string[] = [];
  const stopped: string[] = [];
  const s = new SourceSupervisor((c) => {
    started.push(c.id);
    return () => stopped.push(c.id);
  });
  s.reconcile([new CredEntry("a", { t: "1" }), new CredEntry("b", { t: "2" })]);
  assert.deepEqual([...started].sort(), ["a", "b"]);

  started.length = 0;
  s.reconcile([new CredEntry("a", { t: "9" }), new CredEntry("b", { t: "2" })]);
  assert.deepEqual(stopped, ["a"]);
  assert.deepEqual(started, ["a"]);

  stopped.length = 0;
  s.reconcile([new CredEntry("a", { t: "9" })]);
  assert.deepEqual(stopped, ["b"]);
});

test("无凭证插件也跑一个实例", () => {
  assert.deepEqual(desiredSourceCreds([]).map((c) => c.id), [""]);
});

test("状态板稳定排序，停掉的凭证整体移除", () => {
  const board = new StateBoard(() => "T");
  board.set("src", "b", "running");
  board.set("src", "a", "error", "boom");
  assert.deepEqual(board.snapshot().map((s) => s.credential_id), ["a", "b"]);
  board.removeCred("a");
  assert.deepEqual(board.snapshot().map((s) => s.credential_id), ["b"]);
});

test("事件 id 取自契约（拼错的事件在 trigger 时就被拦）", () => {
  const p = new Plugin({ contract: contract(), name: "demo", token: "t" });
  assert.deepEqual(p.contract.eventIds(), ["ping"]);
});
