# Running Switchboard under systemd: install and upgrade

Status: `REL-010`, 2026-10-22. The procedure was proven by hand on both edges
of the pilot (public and tunnel) on 2026-10-22 and then turned into
`switchboard upgrade`. Paths below are a recommended layout; adapt them, but
keep the ownership.

## Layout: the edge belongs to root

The process runs as its own account. Its code and config are root's, its
state is the account's, and the operator changes any of it through `sudo`.
If the binary or config were writable by the operator's login account, a
stolen shell or SSH key could swap the binary and kill the process for
systemd to relaunch it; if the state were, it could add itself a passkey or
a tunnel connector. A sudo password would then guard nothing.

| What | Where | Owner |
|---|---|---|
| Binary, previous binaries (`switchboard.bak-*`) | `/usr/local/lib/switchboard/` | `root:root` 755 |
| Config (no secrets: tokens are `file:` references) | `/etc/switchboard/config.json` | `root:root` 644 |
| Tokens | `/etc/switchboard/*.token` | `root:switchboard` 640 |
| State: `state.db`, pages, ACME state | `/var/lib/switchboard/` (`StateDirectory`) | `switchboard` 700 |
| Admin socket (`admin.socket`) | `/run/switchboard/admin.sock` | `switchboard` 600 |

The example unit is [`switchboard.service`](switchboard.service).

## Install

```sh
sudo useradd --system --user-group --no-create-home --home-dir /nonexistent \
  --shell /usr/sbin/nologin switchboard
sudo install -d -m 755 /usr/local/lib/switchboard /etc/switchboard
sudo install -m 755 switchboard /usr/local/lib/switchboard/switchboard
sudo install -m 644 config.json /etc/switchboard/config.json
sudo install -o root -g switchboard -m 640 admin.token /etc/switchboard/admin.token
sudo install -m 644 switchboard.service /etc/systemd/system/switchboard.service
sudo systemctl daemon-reload
sudo install -d -o switchboard -g switchboard -m 700 /var/lib/switchboard
sudo -u switchboard /usr/local/lib/switchboard/switchboard migrate up -db /var/lib/switchboard/state.db
sudo systemctl enable --now switchboard
```

Set `admin.socket` to `/run/switchboard/admin.sock`. The CLI then reaches the
running process as the service account, recovery included:

```sh
sudo -u switchboard /usr/local/lib/switchboard/switchboard user list -config /etc/switchboard/config.json
```

## Upgrade

The **new** binary upgrades to itself, because it is the one that knows its
migrations. Never run as root a file that another account can write: copy it
into a root-owned directory and check its SHA-256 there first, then run that
copy.

```sh
SHA=<sha256 published with the build>
T=$(sudo mktemp -d)
sudo cp switchboard-new "$T/switchboard" && sudo chmod 755 "$T/switchboard"
[ "$(sudo sha256sum "$T/switchboard" | cut -d' ' -f1)" = "$SHA" ] &&
  sudo "$T/switchboard" upgrade -unit switchboard \
    -binary /usr/local/lib/switchboard/switchboard -sha256 "$SHA" \
    -config /etc/switchboard/config.json \
    -db /var/lib/switchboard/state.db -user switchboard
sudo rm -rf "$T"
```

`upgrade` copies itself next to the installed binary and checks that copy
again, then only runs the copy. In order: validate the config with the new
binary; stop the unit; as the service account, back up the database when
migrations are pending (`migrate up -backup`) and apply them; keep the
running binary as `switchboard.bak-<sha12>` (the newest `-keep`, default 3);
start the unit; wait until it is active **and** its admin listener answers.
Any failure after the stop puts back the previous binary and, if it migrated,
the database (an older binary refuses a newer schema), and starts the unit
again. A dropped SSH session does not interrupt it; the run is also logged to
`switchboard.upgrade.log` beside the binary.

| Exit | Meaning |
|---|---|
| 0 | the unit runs the new binary (or already did) |
| 2 | refused before the stop: hash, config or plan; the unit was not touched |
| 3 | failed and rolled back; the previous version is active and answers |
| 4 | failed; the previous version was put back but does not answer |

The edge is down for the cutover: a few seconds plus the migrations. That is
the price of a rollback that loses nothing; a migration that fails halfway is
undone before anything restarts. A database backup is never removed by
`upgrade`.

## Not covered

- Reloading routes (`systemctl reload`, SIGHUP) without a restart: install the
  config as root and confirm it took effect by `config_sha256` in
  `GET /_/api/status`, which names the config in effect.
- Installing from a release channel or verifying signatures: `upgrade`
  checks the hash it is given; where that hash comes from is the operator's
  trust decision.
