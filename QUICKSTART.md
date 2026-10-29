# Quickstart

From a clean checkout to a routed app and a process check, with no TLS and no
OIDC provider. Everything stays on loopback. You need Go (version in
[`go.mod`](go.mod)), Python 3 for the demo upstream, and `curl`.

## 1. Demo upstream — terminal 1

```bash
mkdir -p /tmp/ozal-demo
printf 'hello from the routed app\n' > /tmp/ozal-demo/index.html
python3 -m http.server 9000 --bind 127.0.0.1 --directory /tmp/ozal-demo
```

## 2. Ozal — terminal 2

From the repository root:

```bash
go run ./cmd/ozal -config config.quickstart.json
```

## 3. Check the route — terminal 3

```bash
curl -s -H 'Host: app.localhost:8888' http://127.0.0.1:8888/
# hello from the routed app

curl -s -H 'Host: nope.localhost:8888' http://127.0.0.1:8888/
# no route for host "nope.localhost:8888"
# known hosts:
#   app.localhost:8888
```

## 4. Process readiness

```bash
curl -fsS http://127.0.0.1:9090/_/healthz
# ok
```

Status and the administrative console require authentication, even on
loopback. For a persistent console and passkey setup, follow
[first application](docs/operations/first-application.md).

## 5. Stop

`Ctrl-C` in both terminals and remove the demo directory:

```bash
rm -rf /tmp/ozal-demo
```

## Notes

- `*.localhost` resolves to `127.0.0.1` on most systems, so a browser can open
  <http://app.localhost:8888/> directly instead of sending a `Host` header.
- `config.quickstart.json` leaves `admin.token` unset: this minimal trial does
  not grant administrative access. See [secret references](docs/reference.md#secretos)
  for `env:`/`file:` token references.
- The admin listener is loopback-only and rejects any other `Host` (421).
- `dev_errors: true` is local-only: it prints upstream addresses and the route
  list on errors. Never enable it on a public edge.
- The steps are written for Linux/macOS shells; on Windows use the PowerShell
  equivalents (`python -m http.server`, `curl.exe`).
