# Switchboard documentation

## Tracking

- [To implement](TO_IMPLEMENT.md) — the single source of active work:
  priorities, states, blockers and done criteria.
- [Implemented](IMPLEMENTED.md) — closed work with commits and evidence. No
  open tasks.

## Direction

- [Evaluación de desarrollo y stack — 2026-10-27](reviews/2026-10-27-development-and-stack.md)
  — criterio sobre Go, Svelte, SQLite y arquitectura; evidencias de implementación,
  límites de CI/capacidad y prioridades de consolidación.
- [Experiencia completa del producto](architecture/product-experience.md)
  — revisión en español: instalación, primera app y migración de una web
  existente, servidores, consola self-hosted, GitHub, identidad y grupos;
  distingue código actual de propuestas pendientes.
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
- [Deploy product](architecture/deploy-product.md) — accepted plan: projects, CI
  builds in GitHub Actions and a public OIDC deploy endpoint replacing
  `publish-site.py`; decisions D1–D7 taken 2026-10-25.
- [Edge behind Cloudflare Tunnel](architecture/edge-cloudflared.md) — public
  edge decision, naming, ingress rules and the publish workflow.

## Operations

- [Primera aplicación y web existente](operations/first-application.md) — setup
  protegido, sesión privada del operador, alta/configuración, dominio y reversión;
  grupos y sesiones propias.
- [Descarga e instalación](operations/download-install.md) — versiones,
  checksum, helper Linux sin sobrescritura y límites del canal privado.
- [Repositorios GitHub](operations/github-repositories.md) — App propia,
  repositorios seleccionados y confianza de publicación por proyecto.
- [Incorporar un conector](operations/connector-onboarding.md) — clave en su
  host, CSR público, firma, verificación y enrolamiento autorizado.

- [Running under systemd](operations/systemd.md) — the root-owned layout, the
  example unit, install, and `switchboard upgrade` with its rollback.
- [Failure drills](operations/failure-drills.md) — existing local regression
  evidence, safe disposable recovery procedure, and limits still open under
  `LAB-002`.

## Engineering labs

Engineering labs (performance, failure drills, threat model, test quality,
accessibility, conformance, formal methods) are tracked as `LAB-*` entries in
[TO_IMPLEMENT](TO_IMPLEMENT.md). They are engineering experiments, not release
commitments.

- [Trust boundaries and threat model](security/threat-model.md) — source-based
  current-state map and operational limits; not a penetration test.
- [Selected ASVS 5.0.0 L2 mapping](security/asvs-matrix.md) — evidence and
  remaining checks for a selected subset; not a conformance claim.

## For agents

- [`AGENTS.md`](../AGENTS.md) — instructions for any coding agent.
