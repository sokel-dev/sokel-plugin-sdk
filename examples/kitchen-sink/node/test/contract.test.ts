// Copyright 2026 The Sokel Authors
// SPDX-License-Identifier: Apache-2.0

/**
 * The consistency suite, Node side: the contract this implementation reports must equal the golden file.
 *
 * The goal is not to keep one plugin's implementations in several languages in step, which is rarely
 * needed, but to **keep the SDKs' understanding of the protocol in step** — one reference plugin suffices,
 * and no other plugin needs to exist in several languages. The Python side carries the same assertion
 * (sdk-python/tests/test_register_payload.py).
 */

import assert from "node:assert/strict";
import { readFileSync } from "node:fs";
import { test } from "node:test";

import { CONTRACT } from "../src/sokel.gen.js";

// The path is relative to the **compiled** dist/test/, three levels below the plugin root
const golden = JSON.parse(readFileSync(new URL("../../../contract.golden.json", import.meta.url), "utf8"));

test("the contract embedded in the generated output equals the golden file", () => {
  assert.deepEqual(CONTRACT, golden);
});

test("the auth flow's reserved operations are in the contract", () => {
  // The platform panel builds its /credentials/{id}/auth/{step} requests from the contract, and without
  // them it does not know what to send
  const ids = CONTRACT.operations.map((op) => op.id);
  assert.deepEqual(ids.filter((id) => id.startsWith("auth.")), ["auth.start", "auth.poll", "auth.submit"]);
  assert.ok(CONTRACT.operations.filter((op) => op.id.startsWith("auth.")).every((op) => op.internal));
});
