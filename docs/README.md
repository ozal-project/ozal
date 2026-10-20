# Switchboard documentation

## Tracking

- [To implement](TO_IMPLEMENT.md) — the single source of active work:
  priorities, states, blockers and done criteria.
- [Implemented](IMPLEMENTED.md) — closed work with commits and evidence. No
  open tasks.
- [Learning log](learning/LEARNING_LOG.md) — one line per closed task; raw
  material for release write-ups.

## Direction

- [Vision](../VISION.md) — north star, pillars, Cloudflare feature map,
  integration map, year plan.

## Architecture

- [Design pattern](architecture/design.md) — functional core / imperative
  shell, ports and adapters, the request pipeline, Go rules.
- [Stack](architecture/stack.md) — decided, pending and rejected technology
  choices.
- [Durable audit contract](architecture/audit-contract.md) — proposed
  append-only audit, checkpoints and failure semantics.
- [Policy explain](architecture/policy-explain.md) — proposed CLI for
  explaining authorization decisions with the existing pure `Authorize`.
- [Shield challenge](architecture/shield-challenge.md) — proposed
  privacy-friendly proof-of-work challenge at the edge.
- [Identity integration](architecture/identity-integration.md) — options and
  acceptance for apps that already have their own authentication.
- [Edge behind Cloudflare Tunnel](architecture/edge-cloudflared.md) — public
  edge decision, naming, ingress rules and the publish workflow.

## Learning labs

Engineering labs (performance, failure drills, threat model, test quality,
accessibility, conformance, formal methods) are tracked as `LAB-*` entries in
[TO_IMPLEMENT](TO_IMPLEMENT.md). They are learning work, not release
commitments.

## For agents

- [`AGENTS.md`](../AGENTS.md) — instructions for any coding agent.
- [`.agents/skills/switchboard-mentor`](../.agents/skills/switchboard-mentor/SKILL.md)
  — teaching-mode workflow.
