# Switchboard

Self-hosted application access in one Go binary: give an application a hostname,
serve it over HTTPS, and decide who may enter with passkeys or an external OIDC
provider. Built static sites can also be published with deployment history and
rollback. The administrative console is a Svelte app embedded in the binary;
Node is needed to build the UI, not to run the installed gateway.

Switchboard is an alpha project. Features in this checkout can be newer than
those in a tagged release. Owned tunnels, provider independence and external
operator acceptance have explicit remaining gates; consult the version you
install rather than treating future plans as available features.

## Start here

- [Install](docs/operations/download-install.md): choose a version, verify it and install locally or as a Linux service.
- [First application](docs/operations/first-application.md): private console, passkeys, applications, domains and groups.
- [Administration](docs/operations/administration.md): upgrades, backups, recovery, connectors, GitHub and sbctl.
- [Security](docs/security.md): trust boundaries and deployment responsibilities.
- [Configuration and CLI reference](docs/reference.md): advanced options and examples.

For a local trial with a binary built from this checkout, start an application
on port 3000, then:

```sh
switchboard setup -dir ./my-edge -target http://127.0.0.1:3000
```

This creates a new private installation and prints its start and bootstrap
instructions. It does not overwrite an installation, publish DNS or open the
application to the Internet. Use the printed paths and keep credentials local.
For a minimal HTTP routing example, see [QUICKSTART.md](QUICKSTART.md).

## Operating model

The edge owns identity, access decisions, routing and authenticated tunnels.
The host operator owns processes, networking, backups and upstream application
security. Applications retain their internal permissions. The console stays on
loopback/a private socket; remote operators use a private transport.

A public URL needs reachable edge infrastructure, authority over a DNS name and
valid HTTPS. Cloudflare is an optional exterior integration; its API is currently
used for automated DNS-01. The autonomous-publication MVP is documented under
[development](docs/engineering.md#publicacion-autonoma); it is not a promise of
free domains, a hosted service, a global CDN or volumetric DDoS mitigation.

## Development

```sh
go build ./cmd/switchboard
scripts/verify.sh
```

Start with [AGENTS.md](AGENTS.md), [design](docs/architecture/design.md),
[stack](docs/architecture/stack.md) and the [engineering guide](docs/engineering.md).
The console source is in `web/console`, the public site in `site`, and Go owns
the security decisions. Frontend builds use the pinned Node version and lockfiles;
the committed console output is embedded into the Go binary.

Active work: [TO_IMPLEMENT.md](docs/TO_IMPLEMENT.md). Closed work and evidence:
[IMPLEMENTED.md](docs/IMPLEMENTED.md). Long-range direction: [VISION.md](VISION.md).
The [alpha.1 notes](docs/releases/v0.1.0-alpha.1.md) describe that historical
release, not every capability now present on main. Access to release artifacts
depends on the configured distribution channel; this README does not change it.
