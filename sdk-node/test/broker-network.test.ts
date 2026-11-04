import assert from "node:assert/strict";
import { test } from "node:test";

import { discover } from "../src/nats.js";

const access = {
  url: "tls://broker.example.com:4222",
  internal_url: "tls://broker.sokel.svc:4222",
  user: "group", pass: "test-pass", ca: "test-ca", inbox_prefix: "_INBOX_G.group",
  subject: "sokel.plugin.demo.group", min_protocol: 1,
};

async function discovery(network: string, body: unknown) {
  const previous = process.env.SOKEL_BROKER_NETWORK;
  process.env.SOKEL_BROKER_NETWORK = network;
  const oldFetch = globalThis.fetch;
  globalThis.fetch = async (url, init) => {
    assert.equal(url, "https://platform.example.com/api/v1/connect-info");
    assert.equal((init?.headers as Record<string, string>).Authorization, "Bearer test-token");
    return new Response(JSON.stringify(body), { status: 200 });
  };
  try {
    return await discover("https://platform.example.com", "test-token");
  } finally {
    globalThis.fetch = oldFetch;
    if (previous === undefined) delete process.env.SOKEL_BROKER_NETWORK;
    else process.env.SOKEL_BROKER_NETWORK = previous;
  }
}

test("internal discovery selects the internal broker and preserves credentials", async () => {
  assert.deepEqual(await discovery("internal", { nats: access }), { ...access, url: access.internal_url });
});

test("default and public discovery retain the existing broker", async () => {
  for (const network of ["", "public"]) {
    assert.deepEqual(await discovery(network, { nats: access }), access);
    assert.deepEqual(await discovery(network, { transports: { nats: access.url } }), { url: access.url });
  }
});

test("invalid broker network is rejected before discovery", async () => {
  for (const network of ["auto", "INTERNAL", "internal,public"]) {
    await assert.rejects(discovery(network, { nats: access }), /SOKEL_BROKER_NETWORK.*public.*internal/);
  }
});

test("internal discovery never falls back to the public or legacy URL", async () => {
  for (const body of [
    { nats: { url: access.url } },
    { transports: { nats: access.url } },
    { nats: { ...access, internal_url: "" } },
    { nats: { ...access, internal_url: null } },
    { nats: "invalid" }, { nats: [] },
  ]) {
    await assert.rejects(discovery("internal", body), /internal_url/);
  }
});

test("internal discovery rejects malformed, multiple, and non-NATS targets", async () => {
  for (const internal_url of [
    "https://broker.svc:4222", "nats://", "nats://:4222", "nats://broker:0",
    "nats://broker:65536", "nats://broker:bad", "nats://broker:4222/path", "nats://broker:4222?x=1",
    "nats://broker:4222#fragment", "nats://user:pass@broker:4222", "nats://a:4222,nats://b:4222",
    " nats://broker:4222", "nats://broker:4222\n", "nats://broker host:4222", "nats://@broker:4222",
    "nats://broker:", "nats://broker:4222?", "nats://broker:4222#", "nats://[broken]:4222", 42,
  ]) {
    await assert.rejects(discovery("internal", { nats: { ...access, internal_url } }), /internal_url/);
  }
});

test("internal discovery accepts one NATS or TLS host, including IPv6", async () => {
  for (const internal_url of ["nats://broker:4222", "tls://broker.svc", "tls://[::1]:4222"]) {
    assert.equal((await discovery("internal", { nats: { ...access, internal_url } })).url, internal_url);
  }
});

test("internal mode requires discovery even for an existing direct endpoint", async () => {
  const previous = process.env.SOKEL_BROKER_NETWORK;
  process.env.SOKEL_BROKER_NETWORK = "internal";
  try {
    await assert.rejects(discover("nats://broker:4222", "test-token"), /internal.*platform|platform.*internal/);
  } finally {
    if (previous === undefined) delete process.env.SOKEL_BROKER_NETWORK;
    else process.env.SOKEL_BROKER_NETWORK = previous;
  }
});

test("public mode preserves existing direct endpoints", async () => {
  for (const endpoint of ["nats://broker:4222", "tls://broker:4222"]) {
    assert.deepEqual(await discover(endpoint, "test-token", "public"), { url: endpoint });
  }
});
