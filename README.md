# Switchboard

A small reverse proxy, written in Go, that routes incoming requests to local
services by hostname — like an old telephone switchboard connecting each
call to the right line.

## Why

Built to solve a real problem: running several independent projects locally
(each on its own port) makes it easy to collide on ports and annoying to
remember which port serves what. Switchboard lets each project answer to a
name (`magiclink.localhost`, `auditanchor.localhost`, ...) instead of a
port number, and is meant to grow into the same mechanism used to publish
these projects on a real domain later, with automatic TLS.

## Status

Working: host-based routing, health checks, graceful shutdown, and
automatic TLS via ACME are all implemented.

## Roadmap

- [x] Project scaffold
- [x] Host-based routing to local upstreams (plain HTTP)
- [x] Structured request logging
- [x] Graceful shutdown
- [x] Health checks per upstream (unhealthy upstream -> 503, doesn't block startup)
- [x] Automatic TLS via ACME (Let's Encrypt) for real domains
- [ ] Config reload without restart

## Usage

```bash
go run ./cmd/switchboard -config config.json
```

### Local development (plain HTTP)

`config.json`:

```json
{
  "listen": ":8888",
  "routes": [
    { "host": "magiclink.localhost:8888", "target": "http://localhost:8090" },
    { "host": "auditanchor.localhost:8888", "target": "http://localhost:8081", "health_path": "/healthz" }
  ]
}
```

`health_path` defaults to `/` if omitted. An unchecked or healthy upstream
gets traffic; an upstream that fails its check (5xx or unreachable) gets a
503 from Switchboard instead of being proxied to.

### Real deployment (HTTPS via Let's Encrypt)

Set `tls.enabled: true` and point real DNS at the box. Switchboard then
listens on `:80` (ACME challenges + redirect to HTTPS) and `:443` (TLS,
certificates issued and renewed automatically):

```json
{
  "listen": ":8888",
  "tls": {
    "enabled": true,
    "domains": ["magiclink.example.com", "auditanchor.example.com"],
    "cache_dir": "/var/lib/switchboard/certs",
    "email": "you@example.com"
  },
  "routes": [
    { "host": "magiclink.example.com", "target": "http://localhost:8090" },
    { "host": "auditanchor.example.com", "target": "http://localhost:8081", "health_path": "/healthz" }
  ]
}
```

`cache_dir` must be a writable, persistent directory — certificates are
cached there so they aren't re-issued on every restart. `listen` is ignored
in this mode (ports 80/443 are fixed, as ACME and browsers expect).
