# sysinfo

[简体中文](README.zh-CN.md) · For **users** of the plugin, see [`docs/sysinfo.md`](docs/sysinfo.md).

The smallest complete Go plugin: a contract declared in `schema/`, two handlers, and the wiring that
dials back to the platform.

## Layout

| File | What it is |
|---|---|
| `schema/schema.go` | The contract declaration — which operations exist and what they take. **Edit this** |
| `zz_types.go`, `zz_register.go` | Generated types and registration functions. **Do not edit** |
| `main.go` | The handlers plus the connection setup |
| `docs/sysinfo.md` | The user-facing doc, embedded into the binary and reported at registration |

## Development

```bash
sokel-gen            # regenerate after changing schema/
go build ./...
sokel-gen check      # for CI: verifies the generated files are current
```

The contract is generated at build time, not reflected at runtime: a mistake in the declaration is a
compile error. Changing the declaration and forgetting to regenerate turns `sokel-gen check` red —
the most common way codegen fails.

## Running it

```bash
SOKEL_ENDPOINT=http://localhost:8088 SOKEL_TOKEN=skp_xxx go run .
```
