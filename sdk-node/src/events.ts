// Copyright 2026 The Sokel Authors
// SPDX-License-Identifier: Apache-2.0

/**
 * Event sources: the plugin pushes external events to the platform to start workflows (protocol §7).
 *
 * How this differs from operations: an operation is request/reply (the platform calls the plugin);
 * an event is fire-and-forget (the plugin pushes to the platform).
 *
 * Many bots, one replica (protocol v1.3): every registration and heartbeat returns "the subset of
 * credentials assigned to this replica", and the supervisor reconciles against it — one source
 * instance per credential, cancelled when the credential goes away, restarted when its fields change.
 */

import type { FileRuntime, SokelFile } from "./runtime.js";
import { toVars } from "./runtime.js";

export const TRIGGER_SUBJECT = "sokel.trigger";
export const CREDENTIAL_UPDATE_SUBJECT = "sokel.credential.update";

/** One entry of the registration reply's credentials list — a bot identity assigned here. */
export class CredEntry {
  constructor(readonly id: string = "", readonly fields: Record<string, string> = {}) {}

  /** A stable signature of the fields; reconcile uses it to decide "fields changed -> restart". */
  sig(): string {
    return Object.keys(this.fields)
      .sort()
      .map((k) => `${k}=${this.fields[k]}`)
      .join("\n");
  }
}

export interface SourceState {
  source_id: string;
  credential_id?: string;
  status: string;
  error?: string;
  since: string;
}

/** Runtime state per source × credential. Reported with each registration/heartbeat so the panel
 * can show every bot. */
export class StateBoard {
  private readonly m = new Map<string, SourceState>();

  constructor(private readonly now: () => string = () => new Date().toISOString()) {}

  set(sourceId: string, credId: string, status: string, error = ""): void {
    const st: SourceState = { source_id: sourceId, status, since: this.now() };
    if (credId) st.credential_id = credId;
    if (error) st.error = error;
    this.m.set(`${sourceId}|${credId}`, st);
  }

  /**
   * Overwrite only while the instance is still `running`.
   *
   * A source that reported its own status (auth_required, say) and then returned normally should not
   * have that sentence overwritten on the way out: all the panel would show is "exited", and *why*
   * it exited is the half that matters.
   */
  setIfRunning(sourceId: string, credId: string, status: string, error = ""): void {
    const cur = this.m.get(`${sourceId}|${credId}`);
    if (!cur || cur.status === "running") this.set(sourceId, credId, status, error);
  }

  removeCred(credId: string): void {
    for (const [k, v] of this.m) {
      if ((v.credential_id ?? "") === credId) this.m.delete(k);
    }
  }

  snapshot(): SourceState[] {
    return [...this.m.values()].sort((a, b) =>
      a.source_id === b.source_id
        ? (a.credential_id ?? "").localeCompare(b.credential_id ?? "")
        : a.source_id.localeCompare(b.source_id),
    );
  }
}

export type Publish = (subject: string, data: Uint8Array) => void | Promise<void>;

/** The context for a long-running source or a webhook: push events, read and write back
 * credentials, upload attachments, report state. */
export class SourceCtx {
  readonly credential: Record<string, string>;
  readonly credentialId: string;
  readonly sourceId: string;
  /** Set to true when reconcile stops this instance. Long-poll loops run `while (!ctx.stopped)`. */
  stopped = false;

  private readonly token: string;
  private readonly publish: Publish;
  private readonly validEvents: Set<string>;
  private readonly board?: StateBoard;
  private readonly files?: FileRuntime;

  constructor(opts: {
    token: string;
    publish: Publish;
    validEvents?: string[];
    credential?: Record<string, string>;
    credentialId?: string;
    sourceId?: string;
    board?: StateBoard;
    files?: FileRuntime;
  }) {
    this.token = opts.token;
    this.publish = opts.publish;
    this.validEvents = new Set(opts.validEvents ?? []);
    this.credential = opts.credential ?? {};
    this.credentialId = opts.credentialId ?? "";
    this.sourceId = opts.sourceId ?? "";
    this.board = opts.board;
    this.files = opts.files;
  }

  /** Read the credential into a typed shape (same as Ctx.credentialAs on the operation side). */
  credentialAs<T extends object>(): Partial<T> {
    return this.credential as Partial<T>;
  }

  /**
   * Push one event (fire-and-forget).
   *
   * `event` must be a declared event id: a typo fails here rather than turning into a message nobody
   * on the platform claims. That failure mode has no symptoms — the plugin log looks fine and the
   * workflow simply never starts. `eventId` is the idempotency key; the platform deduplicates on
   * (plugin, event, eventId).
   */
  async trigger(event: string, eventId: string, payload: unknown): Promise<void> {
    if (this.validEvents.size > 0 && !this.validEvents.has(event)) {
      throw new Error(`undeclared event "${event}" — declare it under events in manifest.yml`);
    }
    const msg: Record<string, unknown> = {
      token: this.token,
      event,
      payload: toVars(payload),
    };
    if (eventId) msg.event_id = eventId;
    if (this.credentialId) msg.credential_id = this.credentialId;
    await this.publish(TRIGGER_SUBJECT, encode(msg));
  }

  /**
   * Write a patch back to the credential bound to this instance (how a session-style credential
   * refreshes itself while running).
   * The platform is the only store for credentials; a plugin never persists them locally.
   */
  async updateCredential(patch: Record<string, string>): Promise<void> {
    if (!this.credentialId) throw new Error("this source instance has no bound credential to write back to");
    if (Object.keys(patch).length === 0) return;
    Object.assign(this.credential, patch);
    await this.publish(
      CREDENTIAL_UPDATE_SUBJECT,
      encode({ token: this.token, credential_id: this.credentialId, patch }),
    );
  }

  /** Report state (an expired session becomes auth_required); it rides the heartbeat and lights up
   * "needs login" in the panel. */
  reportStatus(status: string, msg = ""): void {
    this.board?.set(this.sourceId, this.credentialId, status, msg);
  }

  async upload(name: string, mime: string, data: Uint8Array): Promise<SokelFile> {
    if (!this.files) return { name, mime, size: data.length, data };
    return this.files.store(name, mime, data);
  }

  async fetch(f: SokelFile): Promise<Uint8Array> {
    if (f.data) return f.data;
    if (!this.files) throw new Error("file runtime not ready");
    return this.files.fetch(f);
  }
}

function encode(v: unknown): Uint8Array {
  return new TextEncoder().encode(JSON.stringify(v));
}

/** A long-running event source. The SDK runs fn in its own task; inside, ctx.trigger pushes. */
export interface Source {
  id: string;
  label: string;
  fn: (ctx: SourceCtx) => Promise<void>;
}

/** Per-credential supervisor: starts, stops and restarts instances to match the assigned set. */
export class SourceSupervisor {
  private readonly running = new Map<string, { stop: () => void; sig: string }>();

  constructor(private readonly start: (c: CredEntry) => () => void) {}

  reconcile(desired: CredEntry[]): void {
    const want = new Map(desired.map((c) => [c.id, c]));
    // Stop: not in the desired set, or its fields changed (stop then start = restart)
    for (const [id, r] of [...this.running]) {
      const c = want.get(id);
      if (c && c.sig() === r.sig) continue;
      r.stop();
      this.running.delete(id);
    }
    // Start: desired but not running
    for (const [id, c] of want) {
      if (this.running.has(id)) continue;
      this.running.set(id, { stop: this.start(c), sig: c.sig() });
    }
  }

  stopAll(): void {
    for (const r of this.running.values()) r.stop();
    this.running.clear();
  }
}

/** Empty (a plugin with no credentials) becomes one bare instance, so both cases take the same
 * code path. */
export function desiredSourceCreds(creds: CredEntry[]): CredEntry[] {
  return creds.length > 0 ? creds : [new CredEntry()];
}
