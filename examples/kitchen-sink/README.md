# kitchen-sink (reference plugin)

[简体中文](README.zh-CN.md) · For **users** of the plugin, see [`docs/kitchen-sink.md`](docs/kitchen-sink.md).

One declaration, two implementations. It is three things at once:

1. **A syntax reference** — every field shape and every optional capability appears once in
   [`sokel.yaml`](sokel.yaml);
2. **The consistency suite** — `python/` and `node/` each implement it, and both must report a
   contract equal to [`contract.golden.json`](contract.golden.json), asserted from three places
   (Go, Python, Node);
3. **A starting point** — delete the parts you do not need.

## Layout

| Path | What it is |
|---|---|
| `sokel.yaml` | The contract declaration (language-neutral). **Edit this** |
| `contract.golden.json` | The golden contract. Refresh it whenever the declaration changes (below) |
| `docs/kitchen-sink.md` | The user-facing doc; inlined into both generated files |
| `python/main.py` | The Python implementation; `python/sokel_gen.py` is generated — **do not edit** |
| `node/src/main.ts` | The Node implementation; `node/src/sokel.gen.ts` is generated — **do not edit** |

## After changing the declaration

```bash
sokel-gen generate ./examples/kitchen-sink
sokel-gen export json ./examples/kitchen-sink > examples/kitchen-sink/contract.golden.json
```

Run both before committing. `sokel-gen check ./examples` is the CI gate: "changed the declaration,
forgot to regenerate" is the most common way codegen fails, and it has no runtime symptom at all.

## Running it

```bash
# Python
cd python && pip install -r requirements.txt
SOKEL_ENDPOINT=nats://localhost:4222 SOKEL_TOKEN=skp_xxx python main.py

# Node
cd node && npm install && npm run build
SOKEL_ENDPOINT=nats://localhost:4222 SOKEL_TOKEN=skp_xxx npm start
```

## Where the consistency suite lives

| Language | Assertion |
|---|---|
| Go | `sokelgen.TestKitchenSink_MatchesGolden` — declaration → contract equals the golden |
| Python | `sdk-python/tests/test_register_payload.py` — the CONTRACT embedded in the generated module |
| Node | `node/test/contract.test.ts` — the same, plus that the auth flow's reserved operations are present |

The goal is not "keep the multi-language implementations of one plugin in step" (rarely a real
need). It is to keep **the SDKs' understanding of the protocol** in step — one reference plugin is
enough, and no other plugin needs to exist in more than one language.
