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
- [Product, code and UX review — 2026-10-21](reviews/2026-10-21-product-code-ux.md)
  — evidence, design direction and proposed operator journeys; active follow-ups
  remain in `TO_IMPLEMENT.md`.

## Architecture

- [Design pattern](architecture/design.md) — functional core / imperative
  shell, ports and adapters, the request pipeline, Go rules.
- [Stack](architecture/stack.md) — decided, pending and rejected technology
  choices.
- [Durable audit contract](architecture/audit-contract.md) — design contract
  with partial local access-audit and checkpoint implementation; no independent
  tamper-evidence claim.
- [Policy explain](architecture/policy-explain.md) — implemented CLI and pure
  authorization explanation, with current scope limits.
- [Shield challenge](architecture/shield-challenge.md) — proposed
  privacy-friendly proof-of-work challenge at the edge.
- [Identity integration](architecture/identity-integration.md) — options and
  acceptance for apps that already have their own authentication.
- [Edge behind Cloudflare Tunnel](architecture/edge-cloudflared.md) — public
  edge decision, naming, ingress rules and the publish workflow.

## Operations

- [Running under systemd](operations/systemd.md) — the root-owned layout, the
  example unit, install, and `switchboard upgrade` with its rollback.
- [Failure drills](operations/failure-drills.md) — existing local regression
  evidence, safe disposable recovery procedure, and limits still open under
  `LAB-002`.

## Learning labs

Engineering labs (performance, failure drills, threat model, test quality,
accessibility, conformance, formal methods) are tracked as `LAB-*` entries in
[TO_IMPLEMENT](TO_IMPLEMENT.md). They are learning work, not release
commitments.

- [Trust boundaries and threat model](security/threat-model.md) — source-based
  current-state map and operational limits; not a penetration test.
- [Selected ASVS 5.0.0 L2 mapping](security/asvs-matrix.md) — evidence and
  remaining checks for a selected subset; not a conformance claim.

## For agents

- [`AGENTS.md`](../AGENTS.md) — instructions for any coding agent.
- [`.agents/skills/switchboard-mentor`](../.agents/skills/switchboard-mentor/SKILL.md)
  — teaching-mode workflow.
