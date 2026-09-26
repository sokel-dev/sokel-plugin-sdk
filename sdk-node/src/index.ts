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

// Only the author-facing API is exported (v0.6.0). The transport internals (NatsTransport, discover,
// stableInstanceId, QUEUE_GROUP, SourceSupervisor, StateBoard, desiredSourceCreds, BufferSink) used to
// be exported too, which made every transport refactor a breaking change for whoever had reached in.
export { Plugin } from "./plugin.js";
export { SDKTooOld } from "./protocol.js";
export { SokelError, NoTransport, PluginError, Retryable, CredentialInvalid, InvalidInput } from "./errors.js";
export type { Call, Config, Invoke, WebhookHandler } from "./plugin.js";
export { Contract, CAP_WEBHOOK, OP_WEBHOOK } from "./contract.js";
export type { ContractData, EventSpec, Field, OperationSpec } from "./contract.js";
export { Ctx, Emitter } from "./runtime.js";
export type { FileRuntime, Frame, Sink, SokelFile } from "./runtime.js";
export { CredEntry, SourceCtx } from "./events.js";
export type { Source } from "./events.js";
export { WebhookRequest, ok, text } from "./webhook.js";
export type { WebhookFrame, WebhookResponse } from "./webhook.js";
export { CONFIRMED, EXPIRED, PENDING, SCANNED } from "./auth.js";
export type { AuthChallenge, AuthHandlers, AuthState } from "./auth.js";
export { env, envOr } from "./env.js";
