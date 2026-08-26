import type { ContractData } from "../src/index.js";

export const SIMPLE_CONTRACT: ContractData = {
  name: "demo",
  operations: [
    {
      id: "greet",
      label: "打招呼",
      inputs: [{ name: "who", type: "string", required: true }],
      outputs: [{ name: "text", type: "string", required: true }],
    },
    {
      id: "stream_it",
      label: "流式",
      stream: true,
      inputs: [],
      outputs: [{ name: "n", type: "number", required: true }],
    },
  ],
  events: [{ id: "ping", fields: [{ name: "at", type: "string" }] }],
};

export function contract(): ContractData {
  return JSON.parse(JSON.stringify(SIMPLE_CONTRACT)) as ContractData;
}
