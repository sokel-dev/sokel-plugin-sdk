// Copyright 2026 The Sokel Authors
// SPDX-License-Identifier: Apache-2.0

/** Ephemeral, group-scoped transport for frames that exceed the broker's message budget. */
import { createHash, randomBytes } from "node:crypto";
import type { MsgHdrs, NatsConnection, Subscription } from "nats";
import { headers as natsHeaders } from "nats";
import { PluginError } from "./errors.js";

const MAX_BYTES = 512 * 1024 * 1024;
const MAX_CHUNK = 1024 * 1024;
export const PAYLOAD_TIMEOUT_MS = 120_000;
const encode = (value: unknown) => Buffer.from(JSON.stringify(value));

export class PayloadError extends PluginError {
  constructor(message: string, override readonly code = "payload_invalid") { super(message); }
}

export function payloadGroup(subject: string): string {
  const parts = subject.split(".");
  if (parts.at(-1) === "large") parts.pop();
  const group = parts.at(-1) ?? "";
  if (!/^[A-Za-z0-9_-]+$/.test(group)) throw new PayloadError("invalid payload group");
  return group;
}

function budget(nc: NatsConnection): number {
  return Math.max(0, (nc.info?.max_payload ?? MAX_CHUNK) - 1024);
}

function remaining(deadline: number): number {
  const ms = deadline - Date.now();
  if (ms <= 0) throw new PayloadError("payload transfer deadline exceeded", "payload_timeout");
  return ms;
}

interface Reference { version: number; subject: string; size: number; sha256: string; chunk_bytes: number }

/** Only the dedicated .large operation subject treats its body as a reference. */
export async function fetchPayload(nc: NatsConnection, group: string, body: Uint8Array,
  deadline = Date.now() + PAYLOAD_TIMEOUT_MS): Promise<Uint8Array> {
  let outer: any;
  try { outer = JSON.parse(Buffer.from(body).toString()); }
  catch { throw new PayloadError("invalid payload reference JSON"); }
  const r = outer?.__sokel_payload as Reference | undefined;
  const prefix = `sokel.payload.${group}.`;
  if (!outer || typeof outer !== "object" || Object.keys(outer).length !== 1 || !r ||
      r.version !== 1 || typeof r.subject !== "string" || !r.subject.startsWith(prefix) ||
      !/^[A-Za-z0-9_-]+$/.test(r.subject.slice(prefix.length)) ||
      !Number.isSafeInteger(r.size) || r.size < 0 || r.size > MAX_BYTES ||
      !Number.isSafeInteger(r.chunk_bytes) || r.chunk_bytes < 1 || r.chunk_bytes > MAX_CHUNK ||
      typeof r.sha256 !== "string" || !/^[a-f0-9]{64}$/.test(r.sha256)) {
    throw new PayloadError("invalid payload reference");
  }
  try {
    const chunks: Uint8Array[] = [];
    const hash = createHash("sha256");
    for (let offset = 0; offset < r.size;) {
      const response = await nc.request(r.subject, encode({ offset }), { timeout: remaining(deadline) });
      if (response.data.length !== Math.min(r.chunk_bytes, r.size - offset)) {
        throw new PayloadError("payload chunk length mismatch");
      }
      chunks.push(response.data);
      hash.update(response.data);
      offset += response.data.length;
    }
    if (hash.digest("hex") !== r.sha256) throw new PayloadError("payload checksum mismatch");
    return Buffer.concat(chunks, r.size);
  } catch (e) {
    // Use a fresh, short timeout: the call's deadline may already have expired.
    await nc.request(r.subject, encode({ cancel: true }), { timeout: 1000 }).catch(() => undefined);
    throw e;
  }
}

/** Send one frame, keeping its producer alive until the receiver has pulled the final chunk. */
export async function publishPayload(nc: NatsConnection, group: string, reply: string, data: Uint8Array,
  supported: boolean, headers?: MsgHdrs, deadline = Date.now() + PAYLOAD_TIMEOUT_MS): Promise<void> {
  const max = budget(nc);
  if (data.length <= max) { nc.publish(reply, data, { headers }); return; }
  if (!supported || data.length > MAX_BYTES || max < 1) {
    throw new PayloadError(`reply exceeds the transport limit (${data.length} bytes); upgrade the platform for chunked payload support`, "payload_too_large");
  }
  const timeout = remaining(deadline);
  const subject = `sokel.payload.${group}.${randomBytes(16).toString("hex")}`;
  const chunkBytes = Math.min(MAX_CHUNK, max);
  const envelope = encode({ __sokel_payload: {
    version: 1, subject, size: data.length, sha256: createHash("sha256").update(data).digest("hex"), chunk_bytes: chunkBytes,
  } });
  if (envelope.length > max) throw new PayloadError("broker payload limit cannot fit a transfer reference", "payload_too_large");
  let offset = 0;
  let sub: Subscription | undefined;
  let timer: ReturnType<typeof setTimeout> | undefined;
  let resolve!: () => void;
  let reject!: (e: unknown) => void;
  const done = new Promise<void>((res, rej) => { resolve = res; reject = rej; });
  // A timeout can occur during flush; attach a rejection observer immediately.
  void done.catch(() => undefined);
  const finish = (e?: unknown) => {
    if (timer) clearTimeout(timer);
    sub?.unsubscribe();
    e ? reject(e) : resolve();
  };
  try {
    sub = nc.subscribe(subject, { callback: (err, msg) => {
      if (err) { finish(err); return; }
      try {
        const request = JSON.parse(Buffer.from(msg.data).toString());
        if (request.cancel === true) {
          msg.respond(new Uint8Array());
          finish(new PayloadError("payload transfer cancelled", "payload_cancelled"));
          return;
        }
        if (!Number.isSafeInteger(request.offset) || request.offset !== offset) {
          msg.respond(new Uint8Array());
          finish(new PayloadError("invalid payload chunk offset"));
          return;
        }
        const chunk = data.subarray(offset, Math.min(offset + chunkBytes, data.length));
        if (!msg.respond(chunk)) throw new PayloadError("payload chunk has no reply subject");
        offset += chunk.length;
        if (offset === data.length) finish();
      } catch (e) { finish(e); }
    } });
    timer = setTimeout(() => finish(new PayloadError("payload transfer deadline exceeded", "payload_timeout")), timeout);
    await Promise.race([nc.flush(), done]);
    remaining(deadline);
    const markedHeaders = natsHeaders();
    if (headers) for (const [name, values] of headers) for (const value of values) markedHeaders.append(name, value);
    markedHeaders.set("Sokel-Payload", "1");
    nc.publish(reply, envelope, { headers: markedHeaders });
    await done;
  } finally {
    if (timer) clearTimeout(timer);
    sub?.unsubscribe();
  }
}
