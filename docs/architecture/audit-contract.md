# Durable audit contract

**Status:** proposed 2026-10-19; partial `AUD-001` implementation in
`internal/audit`, wired on 2026-10-25 for opt-in `access.*` events. Manual
Ed25519 checkpoint creation and local verification are partial `AUD-002`.
Automatic checkpoint cadence, external-retention drills and broad
admin/login/certificate/tunnel coverage remain unimplemented. Tracker:
`AUD-001`, `AUD-002` and `AUD-003` in [TO_IMPLEMENT.md](../TO_IMPLEMENT.md).
This is not a release promise.

The current writer requires POSIX owner-only mode bits. It fails closed on
Windows until a private-ACL adapter is implemented. When configured, it runs
synchronously before the configured access decision is returned or forwarded.

Related: [VISION](../../VISION.md) (transparency log), [AuditAnchor](../../../auditanchor/README.md)
(P3, optional adapter), [events](../../internal/events/events.go) and
[Broker](../../internal/events/broker.go).

## Context

Switchboard publishes access, login, admin and certificate events through
`events.Sink`. The normal process wires `SlogSink` (JSON lines to
stdout/journald) and `Broker` (bounded in-memory history, non-blocking fan-out),
plus an optional notification sink. That is appropriate for live activity and
the console, but it is not an audit log:

- the Broker keeps 200 events and drops events for slow subscribers;
- slog output is a host log with host-level rotation and no Switchboard
  verification contract;
- neither provides a checkpoint that survives a rewrite of the log.

The VISION's strongest claim is a trust system whose state is verifiable even
against its operator. A local hash chain alone does not support that claim: an
actor who can rewrite the file can recompute the chain and truncate its tail.
The guarantee must be relative to a checkpoint retained outside the process's
control. This document defines the smallest contract that can be honestly
called "tamper-evident" and keeps the stronger transparency-log work separate.

## Decision

1. **Separate live activity from durable audit.** `events.Broker` remains the UI
   feed. A new audit writer owns an append-only log; dropping a UI event never
   drops an audit record.
2. **Append-only, owner-only JSONL.** One record per line, global sequence
   number, previous-record hash and record hash. The writer appends and
   `fsync`s; files and directories are `0600`/`0700`.
3. **Signed checkpoints.** The operator can manually sign
   `{v, log_id, seq, hash, time, key_id}` with Ed25519. Automatic cadence and
   clean-shutdown checkpoints remain open. The checkpoint must be copied
   outside the host/admin boundary. A checkpoint stored only next to the log
   does not add trust.
4. **Verify against a retained checkpoint.** `switchboard audit verify` can
   validate a checkpoint signature, match its sequence/hash to the local chain,
   then check the tail. Without an externally retained checkpoint, the tool
   reports internal consistency only. The CLI cannot attest that a supplied
   checkpoint actually came from outside the host.
5. **Explicit failure policy per route.** Protected HTTP access decisions can
   require a durable append before responding or forwarding; if it fails, fail
   closed. Public/static access decisions can use best-effort mode with an
   independent process-log alert. Admin mutations are not yet wired.
6. **RFC 9162 is phase 2.** Hash chain plus signed checkpoints first. Merkle
   tree, signed tree heads, inclusion/consistency proofs and witnesses come
   later; do not call the first version a transparency log.
7. **AuditAnchor is an optional adapter, not a dependency.** It can anchor
   batches or store receipts. If it runs under the same administrator/host, it
   is not an independent witness.

## Threat model

| Capability | Detected? | Limit |
|---|---|---|
| Accidental edit, partial write, disk corruption | Yes, relative to a retained checkpoint | Depends on backup/checkpoint retention |
| Edit/insert/reorder a record after the last checkpoint | Yes | Detects after verification; does not prevent it |
| Truncate the tail after the last checkpoint | Yes | Requires retaining the checkpoint |
| Rewrite the whole log and recompute the chain | Only if a checkpoint outside the attacker's control survives | If the attacker controls log and all checkpoints, no detection |
| Delete log and all checkpoints | No | Out of scope without an external copy/witness |
| Root/kernel compromise during append | No | A compromised writer can forge new records; external witness/remote append needed |
| Forged event content from an application | No | Integrity/order of records, not truth of the claim |
| Confidentiality | No | Restrict file permissions; encrypt only if a separate requirement exists |

The current `switchboard audit verify` checks internal chain consistency and,
when given a checkpoint and pinned key, verifies the checkpoint signature and
chain position. It refuses to read while a writer holds the directory lock.
The result always says `tamper_evident=false` because the CLI cannot prove that
the checkpoint and public-key pin were retained outside the host's control.
The protection becomes meaningful only when the operator independently
preserves both; it is not "prove that nobody edited the log" and it is not
"the operator cannot lie".

Checkpoint v1 signs the UTF-8 bytes of
`switchboard-audit-checkpoint-v1\x00` followed by compact JSON with properties
in this order: `v`, `log_id`, `seq`, `hash`, `time`, `key_id`. `signature` is
standard base64 Ed25519. `switchboard audit keygen` writes the private key as
base64 mode `0600` and the public-key pin as base64; it never prints the
private value. `switchboard audit checkpoint` requires a `file:` or `env:` key
reference and writes one new mode-`0600` checkpoint file. Copy the checkpoint
and pin outside the host before relying on it. Stop the gateway before
checkpointing or verifying; the writer lock refuses concurrent access. Key
rotation policy and automatic checkpoint scheduling are not implemented.

## Record format v1

One JSON object per line. The verifier hashes the exact bytes of the line as
stored; v1 does not require cross-language canonical JSON. A future version may
adopt RFC 8785 if another producer needs it.

Required fields:

- `v`: schema version (`1`).
- `log_id`: random identifier created with the log (not a secret).
- `seq`: monotonically increasing integer starting at 1.
- `time`: RFC 3339 UTC timestamp.
- `type`: stable event type (`access.allowed`, `access.denied`,
  `login.failed`, `route.created`, `cert.renewed`, ...).
- `actor`, `host`, `code`, `reason`, `remote_addr`: same safe fields as
  `events.Event`.
- `prev`: lowercase hex SHA-256 of the previous record, or 32 zero bytes for
  `seq=1`.
- `hash`: lowercase hex SHA-256 of
  `"switchboard-audit-v1\x00" || uint64be(seq) || decode_hex(prev) ||
  uint64be(len(record_without_hash)) || record_without_hash`. `prev` is the
  raw 32-byte digest in the hash input, though its JSON field is lowercase hex.
  `record_without_hash` is the exact compact JSON object bytes with the final
  `hash` property omitted. The length prefix keeps the concatenation
  unambiguous.

Rules:

- Never include tokens, cookies, request bodies, resolved secret references,
  private keys or challenge values.
- Prefer stable decision codes and reviewed generic reasons. `Event.Error` is
  an in-process diagnostic only: `events.Multi`, `events.Broker`, the admin
  event serializer and `SlogSink` remove it before events reach observers.
  The durable audit record also omits it, including for direct appends.
- Admin operation failures use their own stable event types
  (`config.reload_failed`, `route.create_failed`, `route.delete_failed`,
  `pages.failed`) and reviewed codes. They are never represented as successful
  changes. Invalid route host input is omitted from failure events.
- Do not log every static asset by default. Audit security decisions: access
  allow/deny, login, admin mutations, certificate lifecycle, tunnel
  enrolment/revocation/attachment and origin denials. The exact inclusion list
  is a config decision, not a code accident.

## Checkpoints and verification

Checkpoint payload:

```json
{"v":1,"log_id":"...","seq":1024,"hash":"...","time":"2026-10-19T12:00:00Z","key_id":"audit-2026-10"}
```

- Signature: Ed25519 over a domain-separated encoding of the payload. The
  private key comes from `file:`/`env:` like other secrets. The verifier must
  pin the public key outside the same config/admin boundary as the log;
  otherwise an attacker who can replace the key can also replace the
  checkpoints. Keep the pinned key with the external checkpoint copy.
- Key rotation: `key_id` identifies the key; keep old public keys until no
  retained checkpoint uses them.
- Cadence: `checkpoint_every` records and `checkpoint_interval`, whichever
  comes first; also on clean shutdown and by explicit command.
- Storage: local checkpoint file plus `audit checkpoint --out`/`audit export`.
  The operator must copy it to another host, password manager, object store or
  external service. Document that local-only checkpoints do not defend against
  a host-level attacker.
- Retention: retain at least the latest verified checkpoint and the previous
  one; define the retention period for logs and checkpoints. A checkpoint that
  is newer than the available log proves truncation; a checkpoint whose hash
  is absent proves replacement or loss.
- Verification algorithm:
  1. Verify the checkpoint signature and pin.
  2. Find the record with `seq` equal to the checkpoint `seq`; if absent or
     its hash differs, fail.
  3. Walk forward, checking `prev`, `hash`, `seq` continuity and log ID.
  4. Report the last verified sequence and whether the tail is consistent.
  5. Without a checkpoint, verify only internal consistency and label the
     result "not tamper-evident".
- Exit non-zero on any integrity failure. `--json` returns stable fields for
  automation.

## Failure semantics

| Route class | Mode | On append/fsync/checkpoint failure |
|---|---|---|
| Protected HTTP access decisions | `required` (default) | Return 503 before forwarding; alert through the process log; fail startup if the log cannot be opened |
| Public/static access decisions | `best_effort` (default) | Serve; emit to the live event stream; increment a process-local failure count, show unavailable in admin status, log once; events may be missing |
| Admin mutations | Not wired | The live event stream is not a durable audit record |
| Certificate/tunnel control plane | `required` | Refuse the state change if its audit record cannot be persisted; an unaudited trust change is a bug |

- A bounded durable queue may absorb bursts, but it does not replace the
  per-route fallback decision. If the disk is full, the queue fails too.
- `events.Broker` is never the audit source. Do not "fix" audit by increasing
  its history or subscriber buffers.
- On startup, verify the existing log/checkpoint state before serving
  `required` routes. A corrupt or unverifiable audit state must not be silently
  reset.
- Startup never repairs an incomplete tail. With the writer stopped,
  `switchboard audit repair` first verifies the complete prefix and shows a
  dry-run plan; `-apply` atomically publishes the repaired final segment with
  an `audit.repaired` marker. A complete valid JSON record missing only its
  newline is preserved; a complete record with a bad hash and any corruption
  before the final incomplete tail are refused. Verify the chain after repair.
- On unclean shutdown, records since the last `fsync` may be lost; a checkpoint
  can only attest to records it covers. There is no way to detect an event
  that was never persisted. Define the RPO for `required` routes and test it
  with `SIGKILL`; do not describe an unpersisted event as audited.
- Audit alerts must not depend solely on the audit log itself (chicken-and-egg):
  use stderr/slog and the existing notifier, and make the failure visible in
  the admin status.

## Relationship to AuditAnchor and the transparency log

- **AuditAnchor (P3):** optional `AuditSink` adapter. Switchboard appends
  locally first; a worker sends batches to AuditAnchor and stores receipts.
  AuditAnchor can provide Merkle batches and anchoring, but if it runs under
  the same admin it is not an independent witness. It must not be required for
  `v0.1` and must not be in the request path.
- **Transparency log (phase 2):** RFC 9162-style Merkle tree, signed tree
  heads, inclusion and consistency proofs, witnesses. Use reviewed primitives
  (`golang.org/x/mod/sumdb/tlog` or equivalent) rather than inventing Merkle
  math. Only then may the docs use "transparency log" or "verifiable even
  against the operator".
- **Journald/slog:** remains useful as an operational mirror; it is not the
  audit source of truth.

## Alternatives considered

- **Rely on slog/journald only:** rejected for audit; host-level rotation and
  no checkpoint contract. May remain a mirror.
- **SQLite table only:** rejected as the source of truth; an admin can edit it
  and it is not append-only. A SQLite index for querying is acceptable if the
  JSONL remains authoritative.
- **Hash chain only:** rejected as a standalone guarantee; detects accidental
  edits but not recomputation/truncation.
- **AuditAnchor required:** rejected; P3, extra failure domain and not an
  independent witness by itself.
- **Full RFC 9162 now:** rejected for the first release; higher complexity and
  no need before the local checkpoint contract is accepted.

## Acceptance criteria

- Unit tests: record encoding, hash/chain continuity, checkpoint signing and
  verification, tamper/insert/reorder/truncate cases, key rotation, retention.
- Integration: a child-process regression appends and `fsync`s, is killed with
  `SIGKILL`, then reopens, appends again and verifies the chain. The test does
  not simulate a kill during a physical partial write or power loss; a
  deterministic torn-tail fixture covers recovery semantics. Real disk-full
  or read-only failure drills and a check that no secret appears in operator
  output remain open; simulated append failures cover both route modes.
- Performance: measure append+fsync latency and throughput; define a budget
  for `required` routes before enabling them.
- Operations: documented checkpoint export/copy, retention, backup/restore,
  key rotation and incident response.
- Claim check: release notes distinguish `durable audit`,
  `tamper-evident relative to retained checkpoints`, and `transparency log`;
  do not use a stronger term than the evidence supports.

## Open questions

- Which routes require synchronous audit in the first release?
- Default retention for logs/checkpoints and personal data (email/IP)?
- Where does the operator keep the external checkpoint by default?
- Is the first implementation a separate package or part of `events`?
- Do we need a SQLite index for the admin UI, or is `audit verify`/grep enough?
- What is the acceptable latency cost of `fsync` per protected request?

## Release wording

- **v0.1:** if implemented, "durable audit events with local verification
  aids"; do not claim tamper evidence without an external checkpoint.
- **v0.2:** "detects modification or truncation relative to retained
  checkpoints" only after the tests and an external-copy exercise pass.
- **later:** "transparency log / witnesses" only after RFC 9162 proofs and an
  independent witness are real.
