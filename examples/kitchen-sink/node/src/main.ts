// Copyright 2026 The Sokel Authors
// SPDX-License-Identifier: Apache-2.0

/**
 * The kitchen-sink reference plugin (Node / TypeScript implementation).
 *
 * The contract is declared in ../sokel.yaml and sokel.gen.ts is generated from it, so this file is
 * only the implementation — typed throughout. The Python version implements the **same declaration**
 * (../python/main.py), and both must report a byte-identical contract.
 *
 * Run it:
 *
 *     pnpm install && pnpm build
 *     SOKEL_ENDPOINT=http://localhost:8088 SOKEL_TOKEN=skp_xxx node dist/src/main.js
 */

import { createHash } from "node:crypto";
import { ok, text } from "@sokel-dev/plugin-sdk";
import type { AuthChallenge, AuthState, Ctx, Emitter, SourceCtx, WebhookRequest } from "@sokel-dev/plugin-sdk";

import {
  newPlugin,
  onChatStream,
  onEchoAll,
  onFileDigest,
  onHealthCheck,
  triggerHeartbeat,
  triggerMessage,
} from "./sokel.gen.js";
import type {
  ChatStreamIn, ChatStreamOut, Credential, EchoAllIn, EchoAllOut,
  FileDigestIn, FileDigestOut, HealthCheckOut,
} from "./sokel.gen.js";

const p = newPlugin({ name: "kitchen-sink", version: "1.0.0" });

// —— operation: echo every field shape back ——

onEchoAll(p, (ctx: Ctx, in_: EchoAllIn): EchoAllOut => {
  const cred = ctx.credentialAs<Credential>(); // typed credential read, so a raw key cannot be misspelled
  // Structural union: the runtime value *is* the branch (no discriminator), so a shape check is enough
  const doc = in_.doc;
  const docDesc = Array.isArray(doc)
    ? `block array (${doc.length} blocks)`
    : doc
      ? `document object ${JSON.stringify(doc.title)}`
      : "no document";
  const count = in_.count ?? 1;
  return {
    text: in_.text.repeat(Math.max(count, 1)),
    count,
    mode: in_.mode ?? "fast",
    tags: in_.tags ?? [],
    profile: in_.profile,
    points: in_.points ?? [],
    labels: in_.labels ?? {},
    summary:
      `mode=${in_.mode ?? "fast"} ratio=${in_.ratio ?? 0} flag=${in_.flag ?? false} ` +
      `tags=${(in_.tags ?? []).length} points=${(in_.points ?? []).length} ` +
      `extra_keys=${Object.keys(in_.extra ?? {}).sort().join(",")} ${docDesc} region=${cred.region ?? ""}`,
  };
});

// —— operation: a file input fetched lazily, a file output handed back to the platform ——

onFileDigest(p, async (ctx: Ctx, in_: FileDigestIn): Promise<FileDigestOut> => {
  const data = await ctx.fetch(in_.file);
  const sha256 = createHash("sha256").update(data).digest("hex");
  const report = await ctx.upload(
    "digest.json",
    "application/json",
    new TextEncoder().encode(JSON.stringify({ name: in_.file.name, sha256, size: data.length })),
  );
  return {
    name: in_.file.name ?? "",
    sha256,
    size: data.length,
    extra_count: (in_.extras ?? []).length,
    report,
  };
});

// —— operation: streaming ——

onChatStream(p, async (_ctx: Ctx, in_: ChatStreamIn, out: Emitter<ChatStreamOut>) => {
  const frames = Math.max(in_.chunks ?? 3, 1);
  let reply = "";
  for (let i = 0; i < frames; i++) {
    const piece = `${in_.prompt}#${i + 1} `;
    reply += piece;
    out.text(piece); // human-readable increment: visible live while the node runs
    await sleep(50);
  }
  // Finish with typed variables: they flow downstream and are checked against the Outputs contract
  out.vars({ reply: reply.trim(), frames });
});

// —— credential check: the platform's conventional id, called by the credential page's "Test" ——

onHealthCheck(p, (ctx: Ctx): HealthCheckOut => {
  const cred = ctx.credentialAs<Credential>();
  // An unusable credential must come back as **ok=false, not as an error**: an error leaves the
  // platform able to say only "the call failed", not whether the key is missing or upstream said no —
  // and those two call for completely different fixes.
  if (!cred.api_key) return { ok: false, message: "the credential has no API key" };
  if (!cred.api_key.startsWith("sk-")) {
    return { ok: false, message: `an API key looks like sk-…, this one is "${cred.api_key.slice(0, 6)}…"` };
  }
  // A real plugin makes its cheapest upstream call here (GET /me, say) and passes the reply through
  return { ok: true, message: `${cred.base_url ?? ""} / ${cred.region ?? ""} reachable` };
});

// —— webhook: verify the signature, then push a typed event ——

p.registerWebhook(async (ctx: SourceCtx, req: WebhookRequest) => {
  const cred = ctx.credentialAs<Credential>();
  // Verifying the upstream signature is **the plugin's job**: every vendor signs differently, and the
  // platform does not know the upstream — the plugin does
  if (req.header("X-Sokel-Token") !== cred.api_key) return text(401, "bad token");
  const body = req.json<{ id?: string; chat_id?: string; text?: string }>();
  await triggerMessage(ctx, String(body.id ?? ""), {
    chat_id: String(body.chat_id ?? ""),
    text: String(body.text ?? ""),
  });
  return ok();
});

// —— long-running event source: one heartbeat every 30 seconds ——

p.registerSource("heartbeat", "Heartbeat", async (ctx: SourceCtx) => {
  const cred = ctx.credentialAs<Credential>();
  if (!cred.api_key) {
    // With no credential, report "needs login" so the panel lights that row up instead of sitting
    // there quietly doing nothing
    ctx.reportStatus("auth_required", "the credential has not logged in yet");
    return;
  }
  while (!ctx.stopped) {
    await triggerHeartbeat(ctx, `hb-${Math.floor(Date.now() / 1000)}`, {
      chat_id: "system",
      at: new Date().toISOString(),
    });
    await sleep(30_000);
  }
});

// —— collaborative authentication (kind=input: start -> poll -> submit) ——

const pending = new Map<string, string>(); // authId -> the submitted code; use expiring storage in production

p.registerAuth({
  start: (): AuthChallenge => {
    const authId = `demo-${Math.floor(Date.now() / 1000)}`;
    pending.set(authId, "");
    return { authId, prompt: "Type any six digits as the verification code", expiresIn: 300 };
  },
  submit: (_ctx, authId, input) => {
    if (!/^\d{6}$/.test(input)) throw new Error("the code must be six digits");
    pending.set(authId, input);
  },
  poll: (_ctx, authId): AuthState => {
    const code = pending.get(authId);
    // Only a confirmed state carries the session: handing it over earlier makes the platform rewrite
    // the credential row over and over
    return code ? { status: "confirmed", session: { api_key: `sk-demo-${code}` } } : { status: "pending" };
  },
});

function sleep(ms: number): Promise<void> {
  return new Promise((r) => setTimeout(r, ms));
}

await p.run();
