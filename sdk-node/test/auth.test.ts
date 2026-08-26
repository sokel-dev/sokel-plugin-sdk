/** Collaborative authentication: the steps follow from the declaration, and the implementation hangs off
 * reserved operation ids only. */

import assert from "node:assert/strict";
import { test } from "node:test";

import { Plugin } from "../src/index.js";
import type { ContractData } from "../src/index.js";

function base(): ContractData {
  return {
    name: "demo",
    operations: [{ id: "noop", inputs: [], outputs: [] }],
    auth_flow: { kind: "input", steps: ["start", "poll", "submit"] },
  };
}

test("the shape of start, poll and submit", async () => {
  const p = new Plugin({ contract: base(), name: "demo", token: "t" });
  const submitted = new Map<string, string>();
  p.registerAuth({
    start: () => ({ authId: "a1", prompt: "Enter the verification code", expiresIn: 60 }),
    poll: (_ctx, id) =>
      submitted.has(id) ? { status: "confirmed", session: { k: "v" } } : { status: "pending" },
    submit: (_ctx, id, value) => {
      submitted.set(id, value);
    },
  });

  const start = await p.dispatchBuffered({ operation: "auth.start", input: {} });
  assert.equal(start.auth_id, "a1");
  assert.deepEqual(start.challenge, { kind: "input", qr_image: "", prompt: "Enter the verification code" });
  assert.equal(start.expires_in, 60);

  // pending must carry no session; carrying one has the platform rewrite the credential row over and over
  assert.deepEqual(await p.dispatchBuffered({ operation: "auth.poll", input: { auth_id: "a1" } }), {
    status: "pending",
  });

  assert.deepEqual(
    await p.dispatchBuffered({ operation: "auth.submit", input: { auth_id: "a1", input: "123456" } }),
    { ok: true },
  );
  assert.deepEqual(await p.dispatchBuffered({ operation: "auth.poll", input: { auth_id: "a1" } }), {
    status: "confirmed",
    session: { k: "v" },
  });
});

test("implementing more than was declared is refused on the spot", () => {
  // qr has only start and poll; an extra submit is an implementation that is never called
  const data = base();
  data.auth_flow = { kind: "qr", steps: ["start", "poll"] };
  const p = new Plugin({ contract: data, name: "demo", token: "t" });
  assert.throws(
    () =>
      p.registerAuth({
        start: () => ({ prompt: "x" }),
        poll: () => ({ status: "pending" }),
        submit: () => {},
      }),
    /no "submit" step/,
  );
});

test("an auth flow's reserved ids take no part in the single-operation fallback", async () => {
  const p = new Plugin({ contract: base(), name: "demo", token: "t" });
  p.registerAuth({ start: () => ({ prompt: "x" }), poll: () => ({ status: "pending" }) });
  p.register("noop", async (_ctx, _raw, out) => out.vars({ ok: true }));
  assert.deepEqual(await p.dispatchBuffered({ operation: "", input: {} }), { ok: true });
});
