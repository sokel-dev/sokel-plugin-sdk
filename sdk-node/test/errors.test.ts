/** Typed errors travel as code / retryable next to the message (F-328). */

import assert from "node:assert/strict";
import { test } from "node:test";

import { CredentialInvalid, InvalidInput, NoTransport, Retryable, SDKTooOld, SokelError } from "../src/index.js";
import { errorFields } from "../src/errors.js";

test("handler errors carry their code", () => {
  assert.deepEqual(errorFields(new Retryable("429")), { code: "retryable", retryable: true });
  assert.deepEqual(errorFields(new CredentialInvalid("401")), { code: "credential_invalid" });
  assert.deepEqual(errorFields(new InvalidInput("bad chat_id")), { code: "invalid_input" });
  assert.deepEqual(errorFields(new Error("plain")), {});
});

test("SDK errors share a base class and keep their names", () => {
  for (const e of [new NoTransport("x"), new SDKTooOld("y"), new Retryable("z")]) {
    assert.ok(e instanceof SokelError && e instanceof Error);
  }
  assert.equal(new SDKTooOld("y").name, "SDKTooOld");
  assert.equal(new SDKTooOld("y").code, "sdk_too_old");
});
