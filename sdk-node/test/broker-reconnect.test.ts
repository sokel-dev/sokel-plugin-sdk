import assert from "node:assert/strict";
import { once } from "node:events";
import { createServer } from "node:net";
import type { AddressInfo, Socket } from "node:net";
import { test } from "node:test";
import type { NatsConnection } from "nats";

import { connectForever } from "../src/nats.js";
import { Plugin } from "../src/plugin.js";
import { contract } from "./helpers.js";

// The dependency's server pool is inspected only in this test; reconnection below also uses real TCP.
function servers(nc: NatsConnection): string[] {
  return (nc as unknown as { protocol: { servers: { getServers(): { listen: string }[] } } }).protocol.servers.getServers().map(s => s.listen);
}

test("plugin startup uses the internal discovery address with the group's credentials and inbox", { timeout: 5_000 }, async t => {
  const sockets = new Set<Socket>();
  let connected: Record<string, unknown> | undefined;
  let inbox = "";
  let ready!: () => void;
  const registered = new Promise<void>(resolve => { ready = resolve; });
  const server = createServer(socket => {
    sockets.add(socket);
    socket.on("close", () => sockets.delete(socket));
    socket.write('INFO {"server_id":"startup-test","version":"2.10.0","proto":1,"headers":true,"auth_required":true,"max_payload":1048576}\r\n');
    let pending = "";
    let pub: string[] | undefined;
    let sid = "";
    socket.on("data", data => {
      pending += data.toString();
      let end: number;
      while ((end = pending.indexOf("\r\n")) >= 0) {
        const line = pending.slice(0, end);
        pending = pending.slice(end + 2);
        if (pub) {
          if (pub[1] === "sokel.register") {
            const reply = JSON.stringify({ ok: true, name: "demo", subject: "sokel.plugin.demo.group" });
            socket.write(`MSG ${pub[2]} ${sid} ${Buffer.byteLength(reply)}\r\n${reply}\r\n`);
          }
          pub = undefined;
        } else if (line === "PING") socket.write("PONG\r\n");
        else if (line.startsWith("CONNECT ")) connected = JSON.parse(line.slice(8));
        else if (line.startsWith("PUB ")) pub = line.split(" ");
        else if (line.startsWith("SUB _INBOX_G.")) {
          const parts = line.split(" ");
          inbox = parts[1];
          sid = parts.at(-1) ?? "";
        } else if (line.startsWith("SUB sokel.plugin.demo.group ")) ready();
      }
    });
  });
  server.listen(0, "127.0.0.1");
  await once(server, "listening");
  const internal = `nats://127.0.0.1:${(server.address() as AddressInfo).port}`;
  const oldFetch = globalThis.fetch;
  const oldNetwork = process.env.SOKEL_BROKER_NETWORK;
  const oldInstance = process.env.SOKEL_INSTANCE_ID;
  const oldSigint = process.listeners("SIGINT");
  globalThis.fetch = async () => new Response(JSON.stringify({ nats: {
    url: "nats://127.0.0.1:1", internal_url: internal, user: "group", pass: "test-pass", inbox_prefix: "_INBOX_G.group",
  } }));
  process.env.SOKEL_BROKER_NETWORK = "internal";
  process.env.SOKEL_INSTANCE_ID = "broker-network-test";
  t.after(async () => {
    globalThis.fetch = oldFetch;
    if (oldNetwork === undefined) delete process.env.SOKEL_BROKER_NETWORK;
    else process.env.SOKEL_BROKER_NETWORK = oldNetwork;
    if (oldInstance === undefined) delete process.env.SOKEL_INSTANCE_ID;
    else process.env.SOKEL_INSTANCE_ID = oldInstance;
    for (const fn of process.listeners("SIGINT")) if (!oldSigint.includes(fn)) process.removeListener("SIGINT", fn);
    for (const socket of sockets) socket.destroy();
    await new Promise<void>(resolve => server.close(() => resolve()));
  });
  const running = new Plugin({ contract: contract(), name: "demo", endpoint: "https://platform.example.com", token: "test-token" }).run();
  await registered;
  process.emit("SIGTERM");
  await running;
  assert.equal(connected?.user, "group");
  assert.equal(connected?.pass, "test-pass");
  assert.match(inbox, /^_INBOX_G\.group\./);
});

for (const network of ["internal", "public"] as const) {
  test(`${network} broker handles initial and updated INFO without changing network policy`, { timeout: 5_000 }, async t => {
    const sockets = new Set<Socket>();
    const info = { server_id: "broker-test", version: "2.10.0", proto: 1, headers: true, max_payload: 1048576, connect_urls: ["127.0.0.1:65530"] };
    const server = createServer(socket => {
      sockets.add(socket);
      socket.on("close", () => sockets.delete(socket));
      socket.write(`INFO ${JSON.stringify(info)}\r\n`);
      let pending = "";
      socket.on("data", data => {
        pending += data.toString();
        let end: number;
        while ((end = pending.indexOf("\r\n")) >= 0) {
          const line = pending.slice(0, end);
          pending = pending.slice(end + 2);
          if (line === "PING") socket.write("PONG\r\n");
        }
      });
    });
    server.listen(0, "127.0.0.1");
    await once(server, "listening");
    const address = `127.0.0.1:${(server.address() as AddressInfo).port}`;
    let nc: NatsConnection | undefined;
    t.after(async () => {
      await nc?.close();
      for (const socket of sockets) socket.destroy();
      await new Promise<void>(resolve => server.close(() => resolve()));
    });
    nc = await connectForever({ servers: [`nats://${address}`], noRandomize: true, reconnectTimeWait: 1 }, network);
    assert.deepEqual(servers(nc).sort(), (network === "internal" ? [address] : [address, "127.0.0.1:65530"]).sort());

    for (const socket of sockets) socket.write(`INFO ${JSON.stringify({ ...info, max_payload: 2097152, connect_urls: ["127.0.0.1:65529"] })}\r\n`);
    await nc.flush();
    assert.equal(nc.info?.max_payload, 2097152, "ordinary INFO fields still update");
    assert.deepEqual(servers(nc).sort(), (network === "internal" ? [address] : [address, "127.0.0.1:65529"]).sort());

    if (network === "internal") {
      const reconnected = (async () => {
        for await (const status of nc!.status()) if (status.type === "reconnect") return;
        throw new Error("connection closed before reconnect");
      })();
      for (const socket of sockets) socket.destroy();
      await reconnected;
      assert.equal(nc.getServer(), address);
      assert.deepEqual(servers(nc), [address], "reconnect INFO cannot add a public target");
      await nc.flush();
    }
  });
}
