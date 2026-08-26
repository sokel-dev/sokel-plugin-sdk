/** Event sources: validating the declaration, the payload shape, and per-credential reconciliation. */

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

test("the shape of a trigger message", async () => {
  const sent: Array<[string, string]> = [];
  await makeCtx(sent, { credentialId: "cred_1" }).trigger("ping", "evt-1", { at: "now" });
  const [subject, data] = sent[0];
  assert.equal(subject, "sokel.trigger");
  assert.deepEqual(JSON.parse(data), {
    token: "skp_x",
    event: "ping",
    payload: { at: "now" },
    event_id: "evt-1",
    credential_id: "cred_1", // the SDK carries the routing key back; source code never handles it
  });
});

test("a misspelled event name fails on the spot", async () => {
  // otherwise it becomes a message nobody on the platform side claims, with no symptom at all
  await assert.rejects(() => makeCtx([]).trigger("pong", "1", {}), /undeclared event/);
});

test("writing a credential back requires a bound credential", async () => {
  await assert.rejects(() => makeCtx([]).updateCredential({ session: "x" }), /no bound credential/);
});

test("writing a credential back updates the local copy too", async () => {
  const sent: Array<[string, string]> = [];
  const ctx = makeCtx(sent, { credentialId: "cred_1", credential: { session: "old" } });
  await ctx.updateCredential({ session: "new" });
  assert.equal(sent[0][0], "sokel.credential.update");
  assert.deepEqual(JSON.parse(sent[0][1]).patch, { session: "new" });
  // the local copy follows too, or the next tick uses the old value
  assert.equal(ctx.credential.session, "new");
});

test("the supervisor starts and stops per credential, restarting when the fields change", () => {
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

test("a plugin without credentials still runs one instance", () => {
  assert.deepEqual(desiredSourceCreds([]).map((c) => c.id), [""]);
});

test("the status board sorts stably, and a stopped credential is removed entirely", () => {
  const board = new StateBoard(() => "T");
  board.set("src", "b", "running");
  board.set("src", "a", "error", "boom");
  assert.deepEqual(board.snapshot().map((s) => s.credential_id), ["a", "b"]);
  board.removeCred("a");
  assert.deepEqual(board.snapshot().map((s) => s.credential_id), ["b"]);
});

test("event ids come from the contract, so a misspelled event is caught at trigger time", () => {
  const p = new Plugin({ contract: contract(), name: "demo", token: "t" });
  assert.deepEqual(p.contract.eventIds(), ["ping"]);
});
