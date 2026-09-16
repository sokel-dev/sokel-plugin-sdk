# Getting a plugin onto a platform

The SDK gets a plugin written. This is the other half: how the platform learns it exists, and how
the process proves who it is.

## The shape of it

A plugin **dials out**. It connects back to the platform (directly, or through the platform's
broker), so it needs no inbound port, no public IP and no firewall hole. A plugin on a laptop or an
office NAS is reachable by the platform exactly like one in a datacentre.

What it dials with is an **access token belonging to an access group**. The group is a platform-side
row; the token is group-level, so several replicas of the same plugin share one token, adding a
replica needs no new token, and rotating or revoking one affects only that group.

So before the process can connect, the platform needs a row for the plugin and a group under it.

## Creating the row: import the manifest

**Plugins → Import manifest**, paste `manifest.yml`, install. The platform reads the declaration and
creates the row, a default channel, a default access group, and hands back the token. Nothing is
typed into a form, so nothing can disagree with the code.

For a Go plugin whose contract lives in a `schema/` package, export the manifest rather than writing
a second copy by hand:

```bash
sokel-gen export yaml ./my-plugin > my-plugin/manifest.yml
```

Three things follow from how this works:

- **The first import can be minimal** — a name and one operation is enough. Once the process
  connects, its self-reported contract replaces what was imported, so changing the contract means
  restarting the process, not reinstalling. Re-importing the same name is refused on purpose.
- **Validation is the same check as `sokel-gen check`**, so a manifest that generates cleanly imports
  cleanly, and a broken one is reported field by field rather than accepted and half-working.
- **A manifest-imported plugin is third-party by origin**: it lives in the workspace that imported it
  with no catalog entry behind it, so catalog updates, advisories and delisting do not apply. That is
  what you want while developing; publishing to a catalog is a separate path.

## Running it

```bash
SOKEL_ENDPOINT=http://<platform> SOKEL_TOKEN=skp_xxx ./my-plugin
```

The moment it connects, the platform has its self-reported contract: the plugin shows as online and
its operations can be dragged onto a canvas. The operations tab in the UI is **read-only** — it
displays what the process reported. If what you see there disagrees with your code, the first
question is whether an older process is still connected, not where the "sync" button is.

Then exercise one operation from the platform's debug console before wiring anything to it. That
console is not a simulation: a write operation really writes.

## Credentials are not configuration

Upstream credentials are configured **on the platform**, under that plugin, and the platform sends
the resolved fields with each call. They never enter the image, the environment, or the plugin's
disk.

Deployment configuration — upstream address, working directory, proxy switch — goes in environment
variables instead. The test: would this value still be true for the same plugin deployed on another
machine? If not, it is environment, not credential.

## Containerising

- **Fix the working directory and give the replica a stable identity.** The SDK writes an identity
  file into the working directory; if that moves when the container is rebuilt, the old row lingers
  as a ghost replica still claiming work. Mount a volume at a fixed `WORKDIR`, or set
  `SOKEL_INSTANCE_ID` explicitly — an explicit identity always wins.
- **Bake the version in** (`SOKEL_VERSION`), or the replica list shows the version as unknown.
- **Pin the image tag** in whatever the manifest's `deployment` section advertises. `latest` makes
  "which version is running" unanswerable.
- Give the container a fixed hostname, or the replica list identifies it by a container id nobody
  can read.
