/**
 * Wire protocol negotiation: the handshake used to carry no protocol version, so when the platform
 * changed the protocol an older SDK logged "registration failed … retrying" forever and never said
 * "upgrade the SDK".
 */

import assert from "node:assert/strict";
import { test } from "node:test";

import { Plugin, SDKTooOld } from "../src/index.js";
import { WIRE_PROTOCOL, checkProtocol } from "../src/protocol.js";
import { contract } from "./helpers.js";

test("registration reports the protocol and the SDK", () => {
  const body = new Plugin({ contract: contract(), name: "demo", token: "t" }).registerPayload("i", "h", "t");
  assert.equal(body.protocol, WIRE_PROTOCOL);
  assert.match(String(body.sdk), /^node\/\d+\.\d+\.\d+/);
});

test("a platform asking for a newer protocol stops us with an upgrade hint", () => {
  checkProtocol({ min_protocol: WIRE_PROTOCOL });
  checkProtocol({}); // an older platform sends no min_protocol
  assert.throws(() => checkProtocol({ min_protocol: WIRE_PROTOCOL + 1 }), (e: unknown) => e instanceof SDKTooOld && /upgrade/.test((e as Error).message));
});
