# Releasing

[简体中文](RELEASING.zh-CN.md)

**One tag ships all three SDKs** at the same version.

| SDK | Where | How |
|---|---|---|
| Go | nothing to publish | `go get` resolves the git tag — **pushing the tag is the release** |
| Python | PyPI `sokel-plugin-sdk` | GitHub Actions (`.github/workflows/release.yml`) |
| Node | npm `@sokel-dev/plugin-sdk` | same |

Why together: all three implement the same protocol version, and their agreement is held in place by
the golden contract in `examples/kitchen-sink`. Versioning them separately turns "which versions work
with each other" into something a human has to remember — and nobody will, until the day two of them
behave differently.

## One-time setup

### 1. PyPI (do this first — no token needed)

PyPI supports a **pending publisher**: it can be configured before the project exists, so the very
first release needs no token. Go to your account sidebar (not a project page — there is no project
yet) → Publishing → Add a new pending publisher:

| Field | Value |
|---|---|
| PyPI Project Name | `sokel-plugin-sdk` |
| Owner | `sokel-dev` |
| Repository name | `sokel-plugin-sdk` |
| Workflow name | `release.yml` |
| Environment name | `release` (optional, strongly recommended — it narrows who may publish) |

⚠️ A pending publisher **does not reserve the name**: until you actually publish, someone else can
still register it. Publish soon after configuring.

### 2. npm (trusted publishing, already configured)

Both registries now authenticate over OIDC, so **this pipeline holds no long-lived secret at all**.

The npm organization matches the package scope: the package is `@sokel-dev/plugin-sdk`, so the org is
`sokel-dev` — the same name as the GitHub org, one less thing to remember.

The trusted publisher lives on the package's Settings page:

| Field | Value |
|---|---|
| Organization or user | `sokel-dev` |
| Repository | `sokel-plugin-sdk` |
| Workflow filename | `release.yml` (filename only, with the extension) |
| Environment | `release` |
| Allowed actions | `npm publish` |

Two things the workflow does on account of this, both easy to lose in a refactor:

- `id-token: write` on the `publish-npm` job. Without it there is no OIDC token to present.
- `npm install -g npm@latest` before publishing. Trusted publishing needs npm >= 11.5.1 and the npm
  bundled with Node 22 is older; without the upgrade npm falls back to looking for a token and fails
  with a 401, which reads like a credentials problem rather than a version one.

Provenance comes along for free — npm attaches it when publishing over OIDC, so no `--provenance` flag
is needed.

<details>
<summary>If you ever have to publish a new package under a fresh scope</summary>

A trusted publisher is configured on the package's settings page, which does not exist until the
package does — so the very first publish of a brand-new package name needs a token:

- Access Tokens → Generate. A **granular token** is enough: Permissions "Read and write", and under
  "Select packages and scopes" pick the scope (least privilege).
  ⚠️ **Set a real expiration date.** Leaving it blank falls back to *today*, so the token expires the
  day it is created and the release fails with a 401 that looks like a wrong token.
- **The token is shown once.** Paste it straight into GitHub: repository Settings → Environments →
  `release` → Add environment secret, named `NPM_TOKEN`, and give the publish step
  `NODE_AUTH_TOKEN: ${{ secrets.NPM_TOKEN }}`. An environment secret rather than a repository secret:
  only a job running in the `release` environment can read it, so no other workflow — PR-triggered
  ones included — can.
- Then configure the trusted publisher as above, remove the `env:` block, and **delete the secret**.

To check such a token locally:

```bash
echo "//registry.npmjs.org/:_authToken=<token>" >> ~/.npmrc
npm whoami            # prints your username if it works
npm org ls sokel-dev  # lists members if the scope permission is right too
```

Never paste a token into a chat, a shell history or any file in the repository — it is equivalent to
publishing as you.

</details>

`package.json` hard-codes `publishConfig.access = public`: scoped packages default to restricted, and
without that line the first publish fails claiming you need a paid account, which has nothing to do
with the real cause.

### 3. GitHub

Settings → Environments → create `release` (the name must match what you entered above). Adding a
required reviewer here is cheap insurance: publishing cannot be undone — neither npm nor PyPI allows
republishing a version number.

## Cutting a release

```bash
# 1. Bump two version numbers (the tag is the third; CI checks all three agree)
vi sdk-node/package.json      # "version": "0.4.0"
vi sdk-python/pyproject.toml  # version = "0.4.0"

# 2. Run the same gates CI will run
go test ./... && go run ./cmd/sokel-gen check ./examples
(cd sdk-node && npm test)
(cd sdk-python && python -m pytest -q)

# 3. Commit, tag, push
git commit -am "chore: v0.4.0"
git tag v0.4.0
git push origin main --tags
```

`verify` runs every gate first; only then do the npm and PyPI jobs run. Go needs no pipeline at all —
once the tag is pushed, `go get github.com/sokel-dev/sokel-plugin-sdk@v0.4.0` resolves.

For a dry run: Actions → Release → Run workflow, enter a version. That runs `verify` only. **Do this
before the first real release** — it surfaces configuration problems before an irreversible action.
Note that it cannot verify the npm token; only a real publish exercises that path.

## What the gates catch

Every one of them exists because of a failure that only shows up **after** publishing:

| Gate | The problem it catches |
|---|---|
| Version agreement | Package version differs from the tag — afterwards there is no way to trace a package back to a commit |
| `sokel-gen check ./examples` | Declaration changed without regenerating — the shipped SDK disagrees with the examples in the repo |
| Three language suites + golden | One SDK's understanding of the protocol drifted |
| `dist/src/index.js` present in `npm pack --dry-run` | `dist` is not in version control; skip the build and you ship an **empty package** — it installs fine and fails at import |
| `sokel/plugin.py` present in the sdist | The same thing in Python (a misconfigured `packages` ships an empty shell) |
| No hard-coded `__version__` in the package | Distribution metadata and `sokel.__version__` disagreeing — 0.2.0 shipped saying `0.1.0` |
| PyPI publishes the artifacts built by `verify` | Rebuilding could produce something other than what was just checked |

## After publishing

```bash
npm view @sokel-dev/plugin-sdk version
pip index versions sokel-plugin-sdk
go list -m github.com/sokel-dev/sokel-plugin-sdk@v0.4.0
```

Note that PyPI's JSON API is CDN-cached and lags the simple index by a minute or so — an install
succeeding is the real signal, not what one API says.

The scaffolds generated by `sokel-gen init` pin `sokel-plugin-sdk>=0.3` and
`@sokel-dev/plugin-sdk: ^0.3.0`. Bump those two lines in `cmd/sokel-gen/init_lang.go` when the minor
version moves — npm's caret pins the minor on `0.x`, so `^0.3.0` will not match `0.4.0`, and newly
scaffolded plugins would silently install an older SDK.
