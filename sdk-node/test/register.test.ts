/**
 * The registration handshake: declared but not reported is the classic silent failure of any
 * self-reporting mechanism. Everything looks fine on the plugin side and nothing happens on the platform
 * side, so every part of the contract gets an assertion watching it.
 */

import assert from "node:assert/strict";
import { test } from "node:test";

import { Plugin, ok } from "../src/index.js";
import { contract } from "./helpers.js";

test("the registration payload carries every part of the contract", () => {
  const data = contract();
  data.credential_schema = [{ name: "api_key", type: "secret" }];
  data.events_common = [{ name: "at", type: "string" }];
  data.auth_flow = { kind: "qr", steps: ["start", "poll"] };
  data.capabilities = { recency: false };
  data.doc = "# Guide";
  const p = new Plugin({ contract: data, name: "demo", token: "skp_x", version: "1.2.3" });
  const body = p.registerPayload("inst-1", "host-1", "2026-01-01T00:00:00Z") as Record<string, unknown>;

  assert.equal(body.token, "skp_x");
  assert.equal(body.instance_id, "inst-1");
  assert.equal(body.transport, "nats");
  assert.equal(body.version, "1.2.3");
  for (const key of ["operations", "credential_schema", "events", "events_common", "auth_flow", "capabilities", "doc"]) {
    assert.ok(body[key], `the contract's ${key} did not reach the registration payload`);
  }
});

test("registering a webhook handler self-reports the webhook capability", () => {
  // Capability bits follow the facts rather than a declaration: forgetting to declare one should not make
  // the entry button disappear
  const p = new Plugin({ contract: contract(), name: "demo", token: "t" });
  assert.equal((p.registerPayload("i", "h", "T") as any).capabilities, undefined);
  p.registerWebhook(() => ok());
  assert.equal((p.registerPayload("i", "h", "T") as any).capabilities.webhook, true);
});

test("event source states are reported with the registration", () => {
  const p = new Plugin({ contract: contract(), name: "demo", token: "t" });
  p.registerSource("poller", "Poller", async () => {});
  p.board.set("poller", "cred_1", "auth_required", "the session expired");
  const states = (p.registerPayload("i", "h", "T") as any).source_states;
  assert.equal(states.length, 1);
  assert.equal(states[0].status, "auth_required");
  assert.equal(states[0].credential_id, "cred_1");
});
