import assert from "node:assert/strict";
import { once } from "node:events";
import { createServer } from "node:net";
import type { AddressInfo, Socket } from "node:net";
import { test } from "node:test";
import type { NatsConnection } from "nats";

import { connectForever } from "../src/nats.js";

async function broker() {
  const sockets = new Set<Socket>();
  const subscriptions = new Map<number, Set<string>>();
  const state = { connections: 0, pings: 0, dropFirst: false };
  let ping!: () => void;
  const periodicPing = new Promise<void>(resolve => { ping = resolve; });
  const server = createServer(socket => {
    sockets.add(socket);
    const connection = ++state.connections;
    const subs = new Set<string>();
    subscriptions.set(connection, subs);
    socket.on("error", () => undefined);
    socket.on("close", () => sockets.delete(socket));
    socket.write('INFO {"server_id":"recovery-test","version":"2.10.0","proto":1,"max_payload":1048576}\r\n');
    let pending = "";
    socket.on("data", data => {
      pending += data.toString();
      let end: number;
      while ((end = pending.indexOf("\r\n")) >= 0) {
        const line = pending.slice(0, end);
        pending = pending.slice(end + 2);
        if (line === "PING") {
          if (++state.pings > 1) ping();
          if (!state.dropFirst || connection > 1) socket.write("PONG\r\n");
        } else if (line.startsWith("SUB ")) subs.add(line.split(" ")[1]);
      }
    });
  });
  server.listen(0, "127.0.0.1");
  await once(server, "listening");
  return {
    url: `nats://127.0.0.1:${(server.address() as AddressInfo).port}`,
    state, subscriptions, periodicPing,
    async close() {
      for (const socket of sockets) socket.destroy();
      await new Promise<void>(resolve => server.close(() => resolve()));
    },
  };
}

test("default keepalive arrives before the platform lease is at risk", { timeout: 14_000 }, async t => {
  const b = await broker();
  let nc: NatsConnection | undefined;
  t.after(async () => { await nc?.close(); await b.close(); });
  nc = await connectForever({ servers: [b.url] });
  await Promise.race([
    b.periodicPing,
    new Promise<never>((_, reject) => {
      const timer = setTimeout(() => reject(new Error("SDK sent no keepalive within 12 seconds")), 12_000);
      timer.unref();
      t.after(() => clearTimeout(timer));
    }),
  ]);
});

for (const network of ["internal", "public"] as const) {
  test(`${network} connection reconnects on missing PONGs and restores subscriptions`, { timeout: 3_000 }, async t => {
    const b = await broker();
    let nc: NatsConnection | undefined;
    t.after(async () => { await nc?.close(); await b.close(); });
    nc = await connectForever({ servers: [b.url], pingInterval: 20, reconnectTimeWait: 1, reconnectJitter: 0 }, network);
    const sub = nc.subscribe("plugin.calls");
    await nc.flush();
    const reconnected = (async () => {
      for await (const status of nc!.status()) if (status.type === "reconnect") return;
      throw new Error("connection closed before reconnect");
    })();
    b.state.dropFirst = true;
    await reconnected;
    await nc.flush();
    assert.ok(b.subscriptions.get(2)?.has("plugin.calls"));
    assert.equal(sub.isClosed(), false);
  });
}

test("request timeout keeps the healthy socket", { timeout: 3_000 }, async t => {
  const b = await broker();
  let nc: NatsConnection | undefined;
  t.after(async () => { await nc?.close(); await b.close(); });
  nc = await connectForever({ servers: [b.url] });
  await assert.rejects(nc.request("no.reply", undefined, { timeout: 20 }), (err: unknown) => (err as { code: string }).code === "TIMEOUT");
  await nc.flush();
  assert.equal(b.state.connections, 1);
});
