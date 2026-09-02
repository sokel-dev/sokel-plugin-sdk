// Copyright 2026 The Sokel Authors
// SPDX-License-Identifier: Apache-2.0

/**
 * Sokel plugin SDK for Node.js / TypeScript.
 *
 * The contract is declared in a language-neutral manifest.yml; `sokel-gen generate` turns it into
 * typed interfaces and registration functions. This package is the runtime: registration handshake,
 * heartbeat, call dispatch, chunked file transfer, event triggering, webhooks and collaborative
 * authentication.
 *
 * ```ts
 * import { Plugin } from "@sokel-dev/plugin-sdk";
 * import { CONTRACT, onIssuesList } from "./sokel.gen.js";
 *
 * const p = new Plugin({ contract: CONTRACT, name: "gitlab" });
 * onIssuesList(p, async (ctx, in_) => ({ issues: [], count: 0 }));
 * await p.run();
 * ```
 */

export { Plugin } from "./plugin.js";
export type { Call, Config, Invoke, WebhookHandler } from "./plugin.js";
export { Contract, CAP_WEBHOOK, OP_WEBHOOK } from "./contract.js";
export type { ContractData, EventSpec, Field, OperationSpec } from "./contract.js";
export { BufferSink, Ctx, Emitter } from "./runtime.js";
export type { FileRuntime, Frame, Sink, SokelFile } from "./runtime.js";
export { CredEntry, SourceCtx, SourceSupervisor, StateBoard, desiredSourceCreds } from "./events.js";
export type { Source } from "./events.js";
export { WebhookRequest, ok, text } from "./webhook.js";
export type { WebhookFrame, WebhookResponse } from "./webhook.js";
export { CONFIRMED, EXPIRED, PENDING, SCANNED } from "./auth.js";
export type { AuthChallenge, AuthHandlers, AuthState } from "./auth.js";
export { env, envOr } from "./env.js";
export { NatsTransport, QUEUE_GROUP, discover, stableInstanceId } from "./nats.js";
