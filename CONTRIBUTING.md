# Contributing

[简体中文](CONTRIBUTING.zh-CN.md)

## Local development

```bash
go build ./...
go test ./...
gofmt -l .          # must print nothing
```

For the other two SDKs:

```bash
(cd sdk-python && uv venv && uv pip install -e '.[dev]' && python -m pytest -q)
(cd sdk-node && pnpm install && pnpm test)
```

## Regenerate after changing a declaration

Contracts are generated at build time, not reflected at runtime. The examples' `zz_*.go`,
`sokel_gen.py` and `sokel.gen.ts` all come from `sokel-gen`:

```bash
go run ./cmd/sokel-gen generate ./examples
go run ./cmd/sokel-gen check ./examples    # what CI runs
```

CI uses `sokel-gen check` to catch "changed the declaration, forgot to regenerate" — the most common
way codegen fails, and one with no runtime symptom at all.

If you changed `examples/kitchen-sink`, also refresh the golden contract:

```bash
go run ./cmd/sokel-gen export json ./examples/kitchen-sink > examples/kitchen-sink/contract.golden.json
```

That file is asserted from three places (Go, Python, Node). It is what keeps the three SDKs from
drifting apart in how they read the protocol.

## Documentation

English is the default; Chinese versions live beside them as `*.zh-CN.md`. When you change one,
change both — a stale translation is worse than none, because it looks current.

`docs/manifest.md`, `docs/sokel.schema.json` and the kitchen-sink declaration are **embedded in the
`sokel-gen` binary** (`embed.go`), so they are what `sokel-gen docs` and `sokel-gen example` print.
They are referenced, not copied: there is only ever one version of each.

## Checklist before submitting

- [ ] `gofmt -l .` prints nothing; `go vet ./...` is clean
- [ ] All three test suites pass, plus the kitchen-sink golden
- [ ] Generated files are current (`sokel-gen check ./examples`)
- [ ] New exported APIs carry a doc comment that says **why**, not just what
- [ ] Docs changed in both languages

## Releasing

See [RELEASING.md](RELEASING.md): one tag ships all three SDKs (Go from the tag itself, Python and
Node through GitHub Actions).
