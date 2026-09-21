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

Early scaffold. v1 goal: host-based HTTP reverse proxy driven by a static
config file, no TLS yet.

## Roadmap

- [x] Project scaffold
- [ ] Host-based routing to local upstreams (plain HTTP)
- [ ] Config reload without restart
- [ ] Structured request logging
- [ ] Graceful shutdown
- [ ] Automatic TLS via ACME (Let's Encrypt) for real domains
- [ ] Health checks per upstream

## Usage (once implemented)

```bash
go run ./cmd/switchboard -config config.json
```

`config.json`:

```json
{
  "listen": ":8888",
  "routes": [
    { "host": "magiclink.localhost:8888", "target": "http://localhost:8090" },
    { "host": "auditanchor.localhost:8888", "target": "http://localhost:8081" }
  ]
}
```
