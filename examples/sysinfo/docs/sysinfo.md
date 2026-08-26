# sysinfo

Reports basic facts about the machine the plugin runs on, and computes the digest of a file. It is
the smallest complete Sokel plugin: two operations, a file input, an embedded user-facing doc.

## What it does

- **System info** — hostname, OS, architecture, CPU count, Go version, PID, goroutine count, uptime.
  Optionally includes memory statistics. The "note" input is echoed back verbatim, which is a quick
  way to confirm that inputs reach outputs field by field.
- **File digest** — send a file, get its name, MD5 and byte count back. The bytes are fetched lazily
  through the platform's file layer; only the reference travels through the canvas.

## Configuration

| Environment variable | Required | Meaning |
|---|---|---|
| `SOKEL_ENDPOINT` | yes | `nats://broker:4222`, or an `https://` platform URL to discover the broker from |
| `SOKEL_TOKEN` | yes | Access-group token (`skp_…`) |

No credentials: this plugin talks to nothing but the machine it runs on.
