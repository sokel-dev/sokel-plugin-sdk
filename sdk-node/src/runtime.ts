// Copyright 2026 The Sokel Authors
// SPDX-License-Identifier: Apache-2.0

/**
 * Runtime shapes: file references, output frames, the call context.
 *
 * Transport-agnostic — NATS supplies only three callbacks (fetch bytes, store bytes, emit a frame);
 * every other semantic lives here. That is why tests need no broker: a fake file runtime and a fake
 * sink are enough to exercise the whole dispatch path.
 */

/** A file reference. Only the reference travels through the canvas; bytes never inline. */
export interface SokelFile {
  id?: string;
  url?: string;
  name?: string;
  mime?: string;
  size?: number;
  /** Bytes carried directly when there is no platform file layer (unit tests). Not serialized. */
  data?: Uint8Array;
}

/** The fetch/store backend for file bytes, injected by the transport. */
export interface FileRuntime {
  fetch(f: SokelFile): Promise<Uint8Array>;
  store(name: string, mime: string, data: Uint8Array): Promise<SokelFile>;
  /** Stream while reading: memory stays at one chunk regardless of file size. */
  storeStream(name: string, mime: string, src: AsyncIterable<Uint8Array>): Promise<SokelFile>;
}

// Good enough: anything unrecognised becomes application/octet-stream, and the platform stores
// exactly the same bytes either way.
const MIME_BY_EXT: Record<string, string> = {
  ".mp4": "video/mp4", ".webm": "video/webm", ".mkv": "video/x-matroska", ".mov": "video/quicktime",
  ".mp3": "audio/mpeg", ".m4a": "audio/mp4", ".opus": "audio/opus", ".wav": "audio/wav",
  ".json": "application/json", ".txt": "text/plain", ".md": "text/markdown", ".pdf": "application/pdf",
  ".png": "image/png", ".jpg": "image/jpeg", ".jpeg": "image/jpeg", ".webp": "image/webp",
  ".zip": "application/zip", ".srt": "application/x-subrip", ".vtt": "text/vtt",
};

export const FRAME_TEXT = "text";
export const FRAME_JSON = "json";
export const FRAME_VARS = "variables";

export interface Frame {
  kind: string;
  text?: string;
  json?: unknown;
  vars?: Record<string, unknown>;
}

export type Sink = (f: Frame) => void;

/** Typed emitter. Each call is one frame (streaming); for non-streaming operations the SDK buffers
 * the frames and merges them into a single reply. */
export class Emitter<Out> {
  constructor(private readonly sink: Sink) {}

  /** Human-readable text (display / tracing). */
  text(s: string): void {
    this.sink({ kind: FRAME_TEXT, text: s });
  }

  /** Structured JSON (display / tracing). */
  json(v: unknown): void {
    this.sink({ kind: FRAME_JSON, json: v });
  }

  /** Typed output variables (they flow downstream). May be called repeatedly; a later frame
   * overwrites same-named fields. */
  vars(out: Out): void {
    const m = toVars(out);
    if (Object.keys(m).length > 0) this.sink({ kind: FRAME_VARS, vars: m });
  }
}

export function toVars(value: unknown): Record<string, unknown> {
  if (value === null || value === undefined) return {};
  if (typeof value !== "object" || Array.isArray(value)) {
    throw new TypeError(`output must be an object, got ${typeof value}`);
  }
  const out: Record<string, unknown> = {};
  for (const [k, v] of Object.entries(value as Record<string, unknown>)) {
    if (v !== undefined) out[k] = v;
  }
  return out;
}

/** Non-streaming sink: keeps only variables frames and merges them into one output object.
 * text/json frames exist for streaming display only. */
export class BufferSink {
  readonly vars: Record<string, unknown> = {};

  readonly sink: Sink = (f: Frame) => {
    if (f.kind !== FRAME_VARS) return;
    Object.assign(this.vars, f.vars ?? {});
  };
}

/** The context handed to an operation handler: credentials, tracing, file fetch/store. */
export class Ctx {
  readonly credential: Record<string, string>;
  private readonly traceMap: Record<string, string>;
  private readonly files?: FileRuntime;

  constructor(opts: {
    credential?: Record<string, string>;
    trace?: Record<string, string>;
    files?: FileRuntime;
  } = {}) {
    this.credential = opts.credential ?? {};
    this.traceMap = opts.trace ?? {};
    this.files = opts.files;
  }

  /**
   * Tracing context supplied by the platform (run_id / workflow_id / node_id).
   *
   * Calls outside a workflow (console tests, health checks) have none of these and get "" back.
   * **Treat "" as "no retry semantics"**, never as a constant key — doing the latter would
   * deduplicate two independent calls into one.
   */
  trace(key: string): string {
    return this.traceMap[key] ?? "";
  }

  /**
   * Read the credential into a typed shape. It returns Partial<T> rather than T: the platform sends
   * whichever fields that credential row has, and a field the generated Credential marks required
   * can still be missing at runtime (a freshly created credential, one that has not logged in yet).
   * Pretending otherwise only moves the undefined further from where it will explode.
   */
  credentialAs<T extends object>(): Partial<T> {
    return this.credential as Partial<T>;
  }

  async fetch(f: SokelFile): Promise<Uint8Array> {
    if (f.data) return f.data;
    if (!this.files) throw new Error("file runtime not ready");
    return this.files.fetch(f);
  }

  /** Produce a file: hand the bytes back to the platform and get a reference for the output. */
  async upload(name: string, mime: string, data: Uint8Array): Promise<SokelFile> {
    if (!this.files) return { name, mime, size: data.length, data };
    return this.files.store(name, mime, data);
  }

  /**
   * Stream a local file: memory stays at one chunk (1 MiB) regardless of file size.
   *
   * Anything above a few hundred megabytes (video, archives, datasets) belongs here. upload() reads
   * the whole file into memory first, and the symptom of that is a container mysteriously killed by
   * the OOM reaper on large inputs.
   */
  async uploadFile(path: string, name?: string, mime?: string): Promise<SokelFile> {
    const { createReadStream, readFileSync } = await import("node:fs");
    const { basename, extname } = await import("node:path");
    const fname = name ?? basename(path);
    const ftype = mime ?? MIME_BY_EXT[extname(fname).toLowerCase()] ?? "application/octet-stream";
    if (!this.files) {
      const data = readFileSync(path);
      return { name: fname, mime: ftype, size: data.length, data };
    }
    return this.files.storeStream(fname, ftype, createReadStream(path));
  }
}
