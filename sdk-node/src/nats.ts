// Copyright 2026 The Sokel Authors
// SPDX-License-Identifier: Apache-2.0

/**
 * The NATS transport: the plugin **dials out** to the broker (no inbound port, no public IP, no
 * firewall hole).
 *
 * The flow matches the Go and Python SDKs step for step (protocol §1-§7): discover, connect,
 * register (reporting the contract), subscribe (as a queue group), renew by heartbeat, dispatch
 * calls, shut down gracefully. Event-source plugins additionally run a per-credential supervisor.
 */

import { connect, headers as natsHeaders } from "nats";
import type { Msg, NatsConnection } from "nats";
import { hostname } from "node:os";
import { readFileSync, writeFileSync } from "node:fs";
import { createHash, randomBytes } from "node:crypto";

import { OP_WEBHOOK } from "./contract.js";
import { env } from "./env.js";
import { CredEntry, SourceCtx, SourceSupervisor, desiredSourceCreds } from "./events.js";
import type { Plugin } from "./plugin.js";
import type { FileRuntime, Frame, SokelFile } from "./runtime.js";

/** Replicas of a group share one queue: each call goes to exactly one of them. */
export const QUEUE_GROUP = "sokel-workers";
/** Replies and stream frames report which replica answered. */
export const INSTANCE_HEADER = "Sokel-Instance";
/** 1 MiB. Bytes never ride the operation reply (max_payload); they go through the chunk channel. */
const FILE_CHUNK = 1 << 20;
const HEARTBEAT_MS = 20_000;
const RETRY_MS = 8_000;
const REQUEST_TIMEOUT_MS = 8_000;
const FILE_TIMEOUT_MS = 30_000;

const enc = new TextEncoder();
const dec = new TextDecoder();

/** Exchange file bytes with the platform over the same NATS connection. The plugin never needs HTTP
 * access to the platform, so a plugin behind NAT works the same way. */
export class NatsFiles implements FileRuntime {
  constructor(private readonly nc: NatsConnection, private readonly token: string) {}

  async fetch(f: SokelFile): Promise<Uint8Array> {
    const id = f.id || (f.url ? f.url.split("/").pop()! : "");
    if (!id) throw new Error("the file reference has neither id nor url");
    const chunks: Buffer[] = [];
    for (let seq = 0; ; seq++) {
      const resp = await this.nc.request(
        "sokel.file.get",
        enc.encode(JSON.stringify({ token: this.token, id, seq })),
        { timeout: FILE_TIMEOUT_MS },
      );
      const r = JSON.parse(dec.decode(resp.data)) as { error?: string; data?: string; last?: boolean };
      if (r.error) throw new Error(r.error);
      chunks.push(Buffer.from(r.data ?? "", "base64"));
      if (r.last) return Buffer.concat(chunks);
    }
  }

  /** Whole bytes. Delegates to storeStream: the chunking protocol should have one implementation. */
  async store(name: string, mime: string, data: Uint8Array): Promise<SokelFile> {
    return this.storeStream(name, mime, (async function* () { yield data; })());
  }

  /** **Stream while reading**; memory stays at one chunk. The platform already writes into blob
   * storage chunk by chunk. */
  async storeStream(name: string, mime: string, src: AsyncIterable<Uint8Array>): Promise<SokelFile> {
    let uploadId = "";
    let seq = 0;
    let pending = Buffer.alloc(0);
    let done = false;
    const it = src[Symbol.asyncIterator]();
    // Fill a whole chunk before sending: the source decides its own slice size (a fs stream gives
    // 64KB by default), and forwarding those as-is would multiply the round trips more than tenfold.
    while (!done) {
      while (pending.length < FILE_CHUNK) {
        const r = await it.next();
        if (r.done) { done = true; break; }
        pending = Buffer.concat([pending, Buffer.from(r.value)]);
      }
      const chunk = pending.subarray(0, FILE_CHUNK);
      pending = pending.subarray(chunk.length);
      const last = done && pending.length === 0;
      const f = await this.putChunk(name, mime, uploadId, seq, last, chunk);
      if (f.uploadId) uploadId = f.uploadId;
      if (last) {
        if (!f.file) throw new Error("the platform returned no file reference");
        return f.file;
      }
      seq += 1;
    }
    throw new Error("the upload never got a final-chunk reply (should not happen)");
  }

  private async putChunk(
    name: string, mime: string, uploadId: string, seq: number, last: boolean, chunk: Buffer,
  ): Promise<{ uploadId?: string; file?: SokelFile }> {
    {
      {
      const resp = await this.nc.request(
        "sokel.file.put",
        enc.encode(
          JSON.stringify({
            token: this.token,
            upload_id: uploadId,
            name,
            mime,
            seq,
            last,
            data: chunk.toString("base64"),
          }),
        ),
        { timeout: FILE_TIMEOUT_MS },
      );
      const r = JSON.parse(dec.decode(resp.data)) as { error?: string; upload_id?: string; file?: SokelFile };
      if (r.error) throw new Error(r.error);
      return { uploadId: r.upload_id, file: r.file };
      }
    }
  }
}

export class NatsTransport {
  async run(p: Plugin): Promise<void> {
    const target = await discover(p.endpoint, p.token);
    // Transport-level auth prefers SOKEL_NATS_TOKEN and falls back to the access token; a broker
    // without auth ignores it either way.
    const token = env("NATS_TOKEN") || p.token;
    const ca = env("NATS_CA");
    const nc = await connectForever({
      servers: [target],
      name: p.name,
      token: token || undefined,
      maxReconnectAttempts: -1, // reconnect forever; subscriptions restore themselves
      reconnectTimeWait: 2_000,
      waitOnFirstConnect: true, // wait for a broker that is not up yet instead of exiting
      ...(ca ? { tls: { caFile: ca } } : {}),
    });

    const host = hostname();
    const instanceId = stableInstanceId(p.token);
    const startedAt = new Date().toISOString();
    const files = new NatsFiles(nc, p.token);
    let notifySubject = "";

    const register = async (): Promise<{ subject: string; name: string; creds: CredEntry[] }> => {
      const body = p.registerPayload(instanceId, host, startedAt);
      const resp = await nc.request("sokel.register", enc.encode(JSON.stringify(body)), {
        timeout: REQUEST_TIMEOUT_MS,
      });
      const reg = JSON.parse(dec.decode(resp.data)) as {
        ok?: boolean;
        name?: string;
        subject?: string;
        notify_subject?: string;
        error?: string;
        credentials?: Array<{ id?: string; fields?: Record<string, string> }>;
        credential?: Record<string, string>;
        credential_id?: string;
      };
      if (!reg.ok || !reg.subject) throw new Error(`registration refused: ${reg.error ?? "the platform returned no subject"}`);
      if (reg.notify_subject) notifySubject = reg.notify_subject;
      let creds = (reg.credentials ?? []).map((c) => new CredEntry(c.id ?? "", c.fields ?? {}));
      // An older platform only sends the singular form; fold it into a one-element set.
      if (creds.length === 0 && (reg.credential_id || reg.credential)) {
        creds = [new CredEntry(reg.credential_id ?? "", reg.credential ?? {})];
      }
      return { subject: reg.subject, name: reg.name || p.name, creds };
    };

    // A failed first registration is not fatal (broker or platform may still be starting): retry at
    // a fixed interval until it succeeds.
    let first: Awaited<ReturnType<typeof register>>;
    for (;;) {
      try {
        first = await register();
        break;
      } catch (e) {
        console.warn(`[sokel] registration failed (${errText(e)}), retrying in ${RETRY_MS / 1000}s…`);
        await sleep(RETRY_MS);
      }
    }

    const sub = nc.subscribe(first.subject, { queue: QUEUE_GROUP });
    void (async () => {
      for await (const msg of sub) {
        void this.dispatch(p, nc, msg, files, instanceId);
      }
    })();
    console.log(
      `[sokel] connected: plugin "${first.name}" ready, replica ${instanceId} listening on ${first.subject}`,
    );

    let supervisor: SourceSupervisor | undefined;
    if (p.sources.length > 0) {
      supervisor = makeSupervisor(p, nc, files);
      supervisor.reconcile(desiredSourceCreds(first.creds));
      if (notifySubject) {
        // Credential-change notifications use a plain subscription (not a queue group): every
        // replica in the group must hear it, since any assignment may have changed.
        const debounced = debounce(300, async () => {
          try {
            const { creds } = await register();
            supervisor!.reconcile(desiredSourceCreds(creds));
          } catch (e) {
            console.warn(`[sokel] re-register after a credential change failed: ${errText(e)}`);
          }
        });
        const notifySub = nc.subscribe(notifySubject);
        void (async () => {
          for await (const _ of notifySub) debounced();
        })();
      }
    }

    // The heartbeat keeps the replica online. On SIGINT/SIGTERM (docker stop, Ctrl-C) it shuts down
    // gracefully: tell the platform to mark this replica offline right away (seconds), instead of
    // waiting for the heartbeat sweep (45s+).
    await new Promise<void>((resolve) => {
      const timer = setInterval(async () => {
        try {
          const { creds } = await register();
          // Reconcile against the latest assignment each tick: shard moves, credentials added or
          // removed, fields refreshed.
          supervisor?.reconcile(desiredSourceCreds(creds));
        } catch (e) {
          console.warn(`[sokel] heartbeat renewal failed: ${errText(e)}`);
        }
      }, HEARTBEAT_MS);
      const bye = (sig: string) => {
        clearInterval(timer);
        supervisor?.stopAll();
        nc.publish("sokel.unregister", enc.encode(JSON.stringify({ token: p.token, instance_id: instanceId })));
        void nc
          .flush() // make sure the goodbye lands before the connection drops
          .then(() => nc.drain())
          .catch(() => undefined)
          .then(() => {
            console.log(`[sokel] got ${sig}, told the platform we are going offline, exiting`);
            resolve();
          });
      };
      process.once("SIGINT", () => bye("SIGINT"));
      process.once("SIGTERM", () => bye("SIGTERM"));
    });
  }

  private async dispatch(
    p: Plugin,
    nc: NatsConnection,
    msg: Msg,
    files: NatsFiles,
    instanceId: string,
  ): Promise<void> {
    if (!msg.reply) return;
    let call: Record<string, any>;
    try {
      call = JSON.parse(dec.decode(msg.data));
    } catch {
      nc.publish(msg.reply, enc.encode(JSON.stringify({ error: "could not parse the call frame" })));
      return;
    }
    const op = (call.operation as string) ?? "";
    const tag = traceTag(call.trace ?? {});

    // The platform-relayed webhook frame is intercepted before dispatch. An older SDK without this
    // branch falls through to "unknown operation", which the platform translates into "the plugin
    // registered no webhook handler".
    if (op === OP_WEBHOOK) {
      console.log(`[sokel] ← webhook inbound${tag}`);
      const sctx = new SourceCtx({
        token: p.token,
        publish: (subject, data) => nc.publish(subject, data),
        validEvents: p.contract.eventIds(),
        credential: call.credential,
        credentialId: call.credential_id ?? "",
        sourceId: "webhook",
        files,
      });
      const out = await p.handleWebhook(sctx, call.input ?? {});
      nc.publish(msg.reply, enc.encode(JSON.stringify(out)));
      return;
    }

    const h = natsHeaders();
    h.set(INSTANCE_HEADER, instanceId);
    const started = Date.now();
    console.log(`[sokel] ← ${op} started${tag}`);

    if (p.contract.isStream(op)) {
      // Streaming: publish frame by frame to the reply subject; the end frame is mandatory.
      const publishFrame = (f: Frame) => nc.publish(msg.reply!, enc.encode(JSON.stringify(f)), { headers: h });
      try {
        await p.dispatch(call, publishFrame, files);
        console.log(`[sokel] ✓ ${op} done (${Date.now() - started}ms)${tag}`);
      } catch (e) {
        console.warn(`[sokel] ✗ ${op} failed (${Date.now() - started}ms)${tag}: ${errText(e)}`);
        publishFrame({ kind: "error", text: errText(e) });
      }
      publishFrame({ kind: "end" });
      return;
    }

    try {
      const vars = await p.dispatchBuffered(call, files);
      console.log(`[sokel] ✓ ${op} done (${Date.now() - started}ms)${tag}`);
      nc.publish(msg.reply, enc.encode(JSON.stringify(vars)), { headers: h });
    } catch (e) {
      console.warn(`[sokel] ✗ ${op} failed (${Date.now() - started}ms)${tag}: ${errText(e)}`);
      nc.publish(msg.reply, enc.encode(JSON.stringify({ error: errText(e) })), { headers: h });
    }
  }
}

/** One source instance per credential: its ctx is bound to that credential, and trigger carries the
 * credential_id back automatically. */
function makeSupervisor(p: Plugin, nc: NatsConnection, files: NatsFiles): SourceSupervisor {
  return new SourceSupervisor((cred) => {
    const ctxs: SourceCtx[] = [];
    for (const src of p.sources) {
      const sctx = new SourceCtx({
        token: p.token,
        publish: (subject, data) => nc.publish(subject, data),
        validEvents: p.contract.eventIds(),
        credential: cred.fields,
        credentialId: cred.id,
        sourceId: src.id,
        board: p.board,
        files,
      });
      ctxs.push(sctx);
      console.log(`[sokel] event source "${src.id}" started (credential=${cred.id || "(none)"})`);
      p.board.set(src.id, cred.id, "running");
      void src
        .fn(sctx)
        .then(() => {
          if (!sctx.stopped) p.board.setIfRunning(src.id, cred.id, "exited");
        })
        .catch((e) => {
          if (sctx.stopped) return; // stopped by reconcile: stop() already removed the state
          console.warn(`[sokel] event source "${src.id}" exited (credential=${cred.id || "(none)"}): ${errText(e)}`);
          p.board.set(src.id, cred.id, "error", errText(e));
        });
    }
    return () => {
      // JS has no task cancellation: by convention the source loop watches ctx.stopped and exits
      // (a long poll notices on its next tick).
      for (const c of ctxs) c.stopped = true;
      p.board.removeCred(cred.id);
    };
  });
}

/**
 * A single https endpoint becomes the real transport address via the platform's /connect-info.
 * A literal nats:// or tls:// URL skips discovery (local development, offline setups).
 */
export async function discover(endpoint: string, token: string): Promise<string> {
  const ep = (endpoint ?? "").trim();
  if (ep.startsWith("nats://") || ep.startsWith("tls://")) return ep;
  if (!ep.startsWith("http://") && !ep.startsWith("https://")) {
    throw new Error(`invalid endpoint "${endpoint}": expected a platform URL (https://…) or nats://`);
  }
  const url = ep.replace(/\/+$/, "") + "/api/v1/connect-info";
  const resp = await fetch(url, { headers: { Authorization: `Bearer ${token}` } });
  if (!resp.ok) throw new Error(`discovery failed at ${url}: HTTP ${resp.status}`);
  const info = (await resp.json()) as { transports?: Record<string, string> };
  const target = info.transports?.nats;
  if (!target) throw new Error("the platform offers no transport (connect-info.transports is empty)");
  return target;
}

/**
 * A stable replica identity, reused across restarts. Without it every restart leaves the platform
 * holding another ghost row that stays offline forever.
 * In order: 1) SOKEL_INSTANCE_ID; 2) a file in the working directory named after the token's
 * fingerprint; 3) if writing that file fails, host-pid.
 */
export function stableInstanceId(token: string): string {
  const explicit = env("INSTANCE_ID");
  if (explicit) return explicit;
  let file = ".sokel-instance-id";
  if (token) file += "." + createHash("sha256").update(token).digest("hex").slice(0, 8);
  try {
    const existing = readFileSync(file, "utf8").trim();
    if (existing) return existing;
  } catch {
    /* first run: fall through, generate one and write it out */
  }
  const id = `${hostname()}-${randomBytes(4).toString("hex")}`;
  try {
    writeFileSync(file, id + "\n");
  } catch {
    return `${hostname()}-${process.pid}`;
  }
  return id;
}

async function connectForever(opts: Parameters<typeof connect>[0]): Promise<NatsConnection> {
  for (;;) {
    try {
      return await connect(opts);
    } catch (e) {
      console.warn(`[sokel] could not connect to the platform (${errText(e)}), retrying in ${RETRY_MS / 1000}s…`);
      await sleep(RETRY_MS);
    }
  }
}

function debounce(ms: number, fn: () => void): () => void {
  let timer: NodeJS.Timeout | undefined;
  return () => {
    if (timer) clearTimeout(timer);
    timer = setTimeout(fn, ms);
  };
}

function traceTag(trace: Record<string, string>): string {
  const parts = ([["run_id", "run"], ["workflow_id", "wf"], ["node_id", "node"]] as const)
    .filter(([key]) => trace[key])
    .map(([key, label]) => `${label}=${trace[key]}`);
  return parts.length > 0 ? ` [${parts.join(" ")}]` : "";
}

function errText(e: unknown): string {
  return e instanceof Error ? e.message : String(e);
}

function sleep(ms: number): Promise<void> {
  return new Promise((r) => setTimeout(r, ms));
}
