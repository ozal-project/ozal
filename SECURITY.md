# Security policy

Switchboard is an alpha project and the only supported code is the latest
`main` and the most recent tagged release. Older tags do not receive fixes.

## Reporting a vulnerability

Please report vulnerabilities privately; do not open a public issue or pull
request for them.

Use GitHub's private vulnerability reporting: on the repository page choose
**Security → Report a vulnerability**. The report stays visible only to the
maintainers until a fix is released.

Helpful details: the version or commit, how the deployment is configured
(direct or behind a proxy, which login method), the steps to reproduce, and
what an attacker gains. Do not include real secrets, session cookies or
private keys; describe where they are instead.

## What to expect

This is a small project maintained by one person, so these are goals rather
than guarantees:

- acknowledgement within 7 days;
- an initial assessment within 14 days;
- a fix or a documented mitigation before public disclosure, coordinated with
  you. Credit is given in the release notes unless you prefer otherwise.

## Scope

In scope: the gateway, the console and admin API, the native login and
sessions, tunnels and connectors, Pages deployment, and the release artifacts
and installers in this repository.

Out of scope: weaknesses that need an already-compromised host or administrator
account, denial of service by raw traffic volume, and the behavior of
applications placed behind Switchboard (it does not replace their own
authorization). The deployment limits are described in
[docs/security.md](docs/security.md).
