# Publishing a plugin to the catalog

A plugin that runs for you is installed by importing its manifest (see `platform.md`). A plugin
other people should find and install from their platform's marketplace needs an **entry in the
catalog**: [github.com/sokel-dev/sokel-registry](https://github.com/sokel-dev/sokel-registry),
published at <https://sokel-dev.github.io/sokel-registry>. Platforms read that site as a refresh
source and ship a snapshot of it; an entry there is what turns "a repository somewhere" into a card
in the marketplace with an install button.

Everything in the catalog changes through pull requests. CI checks the mechanics (the same
admission as `sokel-gen check`, plus a version gate); a code owner reviews what CI cannot.

## Before the pull request

- **The source is public and the artifact runs.** Publish an image in a public registry (ghcr,
  Docker Hub, …) or a release binary, and keep the source that built it public: reviewers check
  that the artifact comes from the source you link.
- **`deployment.targets[].ref` is pinned.** `ghcr.io/acme/foo:1.2.0`, better
  `ghcr.io/acme/foo:1.2.0@sha256:…` — never `latest`. A tag that moves makes the catalog's version
  gate and its security advisories meaningless, since both are keyed by version.
- **The manifest carries its identity**: `plugin.org` (your GitHub user or organization name,
  lowercase; `sokel` is reserved for plugins maintained in the Sokel project), `plugin.name` (equal
  to the directory name), `plugin.version` (`vX.Y.Z`), `plugin.label` and `plugin.desc`.
- **`sokel-gen check .` is green.** The catalog runs the same checks and rejects what it rejects.
- Optional, recommended: `locales/<lang>.json` (source string → translation, see `manifest.md`),
  a `README.md` next to the manifest (the usage doc; it is shown on the detail page), and an icon
  (`plugin.icon`: `icon.svg` / `icon.png` in the entry, or `brand:<id>` for a built-in mark listed
  in the catalog's `site/brands.js`). SVG: no scripts, event handlers or external references, at
  most 32 KB; PNG: at least 128×128, at most 64 KB, about square. Platforms serve the icon
  themselves, so it never loads from your server.

## Submitting

1. Fork the catalog and create `plugins/<org>/<name>/` with `manifest.yml` (`sokel-gen export yaml`
   produces it from a Go `schema/` package), plus `locales/`, `README.md` and the icon if you have
   them.
2. **First plugin of a new org:** in the same pull request add `/plugins/<org>/ @your-handle` to
   `.github/CODEOWNERS`. From then on, changes under your org need your approval, and nobody else's
   pull request can change it.
3. Check locally — this is exactly what CI runs:
   ```bash
   go run ./cmd/build-index -site _site .
   ```
4. Open the pull request and fill in the template: source repository, how it runs and where it is
   published, which credentials it asks for and where they go, the icon.

What reviewers look at, beyond CI: the org really is yours and the name does not impersonate
another product or publisher; the credentials it asks for are what it needs and the docs say where
they go; the image or binary comes from the linked public source; the usage doc tells a stranger
how to get it running.

## A new version

Change the entry and **raise `plugin.version`**. CI compares the entry with `main` and refuses a
change that keeps or lowers the version (a changed `deployment.targets[].ref` or a comment alone
does not count as a change). Point the image at the new version. Your org's code owner approves it.

This is worth automating: tag the plugin repository, let CI build and push the image, rewrite the
entry (contract from `sokel-gen export yaml`, new version, image pinned by the digest the build
printed) and open the pull request. The official plugins do exactly that; their
[release workflow](https://github.com/sokel-dev/sokel-official-plugins/blob/main/.github/workflows/release.yml)
and the small script it uses are a template.

## After the merge

The publish workflow rebuilds `index.json` and the page within minutes. Platforms that use the site
as a refresh source see the entry at their next refresh (every 12 hours, or "sync now" in platform
settings); it shows in the marketplace with a "remote source" mark until the platform's built-in
snapshot, which follows platform releases, carries it. Installing fetches the manifest from the
catalog and derives the contract the same way as a local import.

## A problem with a published version

- **Already public, or low risk:** a pull request adding an entry to `advisories.json`
  (syntax in the catalog's `ADVISORIES.md`). `warn` flags the version; `block` makes platforms
  refuse its calls. List the affected range, for example `["<v1.2.0"]`, and fix with a new version.
- **Not public yet:** GitHub's private vulnerability reporting on the catalog repository, so it
  can be fixed before it is announced.

An advisory reaches platforms at their next refresh, and every platform release after that carries
it in the built-in snapshot.

## Delisting

Remove the entry's directory. Installed copies keep working but get no more updates or advisories.
Delisting needs a maintainer's approval.
