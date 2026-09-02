// Copyright 2026 The Sokel Authors
// SPDX-License-Identifier: Apache-2.0

/**
 * The runtime view of a contract (wire protocol §5).
 *
 * A contract is **data**: `sokel-gen` renders a CONTRACT constant from manifest.yml, and the runtime
 * only looks things up in it and reports it. So there is no second Field validator here — that
 * would be a second definition of the contract, and two definitions drift.
 */

/** One input/output field (protocol §5's Field). Shape only; validation lives in sokel-gen. */
export interface Field {
  name: string;
  label?: string;
  type: string;
  types?: string[];
  required?: boolean;
  default?: unknown;
  desc?: string;
  options?: Array<string | { value: string; label?: string }>;
  fields?: Field[];
  valueType?: Field;
  itemType?: string;
  goType?: string;
  opaque?: boolean;
  oneOf?: Array<{ name: string; label?: string; type: string; fields?: Field[] }>;
}

export interface OperationSpec {
  id: string;
  label?: string;
  desc?: string;
  stream?: boolean;
  internal?: boolean;
  timeoutSec?: number;
  inputs: Field[];
  outputs: Field[];
}

export interface EventSpec {
  id: string;
  label?: string;
  desc?: string;
  fields: Field[];
}

export interface AuthFlowSpec {
  kind: "qr" | "input" | "oauth";
  steps?: string[];
}

/** The shape of the generated CONTRACT. Keys match the registration payload (protocol §3)
 * verbatim, so they are reported as-is with no translation step. */
export interface ContractData {
  name?: string;
  label?: string;
  desc?: string;
  version?: string;
  operations: OperationSpec[];
  credential_schema?: Field[];
  events?: EventSpec[];
  events_common?: Field[];
  auth_flow?: AuthFlowSpec;
  oauth?: { provider: string; scopes?: string[] };
  capabilities?: Record<string, boolean>;
  doc?: string;
  doc_url?: string;
}

/** Reserved operation ids (the auth flow). They contain a dot, which business ids cannot produce
 * (a business id must match ^[a-z][a-z0-9_]*$). */
export const OP_AUTH_START = "auth.start";
export const OP_AUTH_POLL = "auth.poll";
export const OP_AUTH_SUBMIT = "auth.submit";

/** The special operation name for platform-relayed webhooks (it reuses the call frame, §7b). */
export const OP_WEBHOOK = "__webhook__";

/** Capability bit: registering a webhook handler *is* the declaration; the author does not repeat it. */
export const CAP_WEBHOOK = "webhook";

export class Contract {
  constructor(readonly data: ContractData) {}

  operations(): OperationSpec[] {
    return this.data.operations ?? [];
  }

  operation(id: string): OperationSpec | undefined {
    return this.operations().find((op) => op.id === id);
  }

  isStream(id: string): boolean {
    return this.operation(id)?.stream === true;
  }

  eventIds(): string[] {
    return (this.data.events ?? []).map((e) => e.id);
  }

  /** The contract half of the registration payload. Empty values are omitted (the protocol makes
   * every new field optional). */
  payload(): Record<string, unknown> {
    const out: Record<string, unknown> = { operations: this.operations() };
    const d = this.data;
    if (d.credential_schema?.length) out.credential_schema = d.credential_schema;
    if (d.events?.length) out.events = d.events;
    if (d.events_common?.length) out.events_common = d.events_common;
    if (d.auth_flow) out.auth_flow = d.auth_flow;
    if (d.oauth) out.oauth = d.oauth;
    if (d.capabilities && Object.keys(d.capabilities).length) out.capabilities = d.capabilities;
    if (d.doc) out.doc = d.doc;
    if (d.doc_url) out.doc_url = d.doc_url;
    return out;
  }
}
