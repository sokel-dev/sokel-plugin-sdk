/**
 * 契约的运行时视图（线协议 §5）。
 *
 * 契约本身是**数据**：`sokel-gen` 从 sokel.yaml 生成一份 CONTRACT 常量，运行时只是查它、上报它。
 * 所以这里不重新定义一套 Field 校验器——那会变成契约的第二份定义，而两份定义迟早会漂。
 */

/** 一个入/出参字段（协议 §5 的 Field）。这里只声明形状，不做校验——校验在 sokel-gen。 */
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

/** 生成物 CONTRACT 的形状。键名与注册载荷（协议 §3）同名，直接上报，不做转换。 */
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

/** 保留操作 id（认证流）。带点号，业务 id 产生不出来（业务 id 限定 ^[a-z][a-z0-9_]*$）。 */
export const OP_AUTH_START = "auth.start";
export const OP_AUTH_POLL = "auth.poll";
export const OP_AUTH_SUBMIT = "auth.submit";

/** 平台代收 webhook 的特殊操作名（复用调用帧，见协议 §7b）。 */
export const OP_WEBHOOK = "__webhook__";

/** 能力位：注册了 webhook 处理器就是支持，不靠作者手动声明。 */
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

  /** 契约部分的注册载荷。空值一律省略（协议：新字段一律 optional）。 */
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
