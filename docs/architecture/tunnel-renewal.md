# Automatic private pilot leaf renewal

`switchboard tunnel renew` is a Linux operator command inside the existing Go
binary. It renews the edge and connector leaves as one batch when either has
seven days or less remaining. Leaves last 30 days. The command uses Go's
`crypto/ed25519`, `crypto/x509`, TLS and JSON packages; it does not require
Python, OpenSSL or a separate public renewal server.

The homelab holds the private CA and initiates authenticated SSH to the VPS
with the existing dedicated key and strict host-key verification. The remote
binary runs `tunnel renew-agent -role edge`, reading one bounded JSON request
from SSH stdin. It never exposes a network listener for the CA or renewal.
Only CSRs, public certificates and public metadata cross hosts. Each role
generates its new private key in its own private `identities/<id>` directory.

## Transaction

1. Stage both identities without changing live files or authorization. The
   signer validates CSR possession and fixes SAN/EKU by role. The host validates
   the chain, exact DNS identity, remaining lifetime and matching local key.
2. Persist `pending.json` before live mutations. File and directory fsync make
   intent durable. An OS file lock prevents simultaneous controllers and is
   released automatically if the controller dies.
3. Atomically switch the edge's `current` symlink and restart its user unit.
4. Replace the connector registry entry using `connector rotate -name portfolio
   -expected SHA256 -cert PUBLIC_CERT`. Retiring the expected old row and
   inserting the new row happen in one SQLite transaction; history is retained.
   A revocation or another rotation makes the comparison fail.
5. Switch the connector's `current` symlink, restart its user unit and verify
   the complete tunnel's HTTP content against the homelab's Portfolio. Both
   roles change before this probe so a pair that expired while offline can
   recover together.
6. Remove the journal only after verification. A lost SSH reply is resolved by
   checking current fingerprints/pointers; retries do not duplicate enrolment.

On a failed candidate, restore previous pointers and the previous enrolment
only while the current fingerprint still belongs to this transaction. An
operator revocation or unrelated replacement takes precedence over rollback.
If rollback cannot contact the VPS, keep the journal and fail visibly; the
next execution resumes that batch before creating another one. Ordinary
renewal causes a brief reconnect; this is not zero-downtime certificate reload.

## Commands and operation

On the homelab, first initialize pointers without restarting services:

```sh
~/.local/share/switchboard-tunnel-connector/switchboard tunnel renew -init
```

Normal check/renewal and an explicit forced maintenance exercise:

```sh
~/.local/share/switchboard-tunnel-connector/switchboard tunnel renew
~/.local/share/switchboard-tunnel-connector/switchboard tunnel renew -force
```

The timer/unit are owned by [Server Ops](../../../server-ops/deploy/hetzner-tunnel/README.md).
It runs daily, catches missed runs, and retries failed attempts hourly. Linger
allows execution without an interactive session. The renewal command uses
only the existing SSH access and new pilot units, without sudo or firewall/DNS
changes. `status.json` and a pending journal live under
`~/.local/share/switchboard-tunnel-renew` with private permissions. Journals
contain paths and public certificate metadata, never private key material.

The one-year CA is a trust anchor and is not silently replaced by leaf renewal.
The command warns when fewer than 60 days remain and refuses to issue a leaf
when the CA has 37 days or less. A planned CA rollover distributes new trust
to both roles separately. Existing identity versions are retained privately
for recovery; this command does not delete historical keys.

## Validation

Registry tests cover stale fingerprints, operator revocation, duplicate
replacement rollback and simultaneous rotations. Native PKI tests cover
CSR/role issuance, key mismatch, lifetime and CA-expiry rejection. Coordinator
tests cover lost replies, interrupted journals, failed candidates, operator
replacements and both expired roles. The real-host maintenance exercise must
also be checked after deploying a new renewal binary.
