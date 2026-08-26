# Kitchen sink

A **reference plugin**: it does no real work, but it demonstrates one of everything a Sokel plugin
can declare. Use it to check connectivity, to look up how something is written, or as the starting
point for a plugin of your own.

## Credentials

| Field | Meaning |
|---|---|
| API key | Required, stored masked |
| Service URL | Defaults to `https://api.example.com` |
| Region | China / Singapore |

The credential row has a "log in" button. Clicking it makes the plugin pose a challenge — here,
typing back a verification code (any six digits count as valid) — and once the panel polls a
confirmation, the session is written back into the credential.

## Operations

- **Echo every shape** — one input of each shape (text, number, boolean, enum, array, nested object,
  dynamic keys, structural union, passthrough JSON), echoed back. It answers the question "what does
  the runtime value look like for the thing I declared?".
- **Credential check** — the platform's conventional `health_check`: what the credential page's
  "Test" button calls. Here it checks that the API key starts with `sk-`, and reports an unusable
  credential with a reason rather than an error.
- **File digest** — send a file, get its SHA-256 and byte count back, plus a report file. Files move
  through the canvas as references; the bytes travel over the chunk channel between plugin and
  platform.
- **Streaming reply** — emits text frame by frame (visible live while the node runs) and hands
  downstream nodes a typed output object at the end.

## Events

The plugin pushes two kinds of event, either of which can trigger a workflow:

- **Message received** — pushed from an inbound webhook (point the credential's webhook entry at your
  upstream system).
- **Heartbeat** — the event source pushes one every 30 seconds, which is how you confirm the
  "plugin pushes, platform starts a run" path is alive.

Both carry `chat_id`. It is a common field, so a trigger node exposes it directly in its output.

## Webhooks

Open a webhook entry on the credential row and give the address to the upstream system. The plugin
checks the `X-Sokel-Token` header against the API key in the credential and pushes an event only if
it matches; otherwise it answers 401.
