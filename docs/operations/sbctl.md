# Operating an edge with sbctl

Status: `REL-013`, 2026-10-28. `sbctl` runs on the operator's machine and
talks to edges over SSH. It is never installed on a server and the edge
binary does not contain it. Every command prints its plan first and changes
nothing without `-apply`; the ones that need root run one script under one
`sudo` prompt and roll back on their own.

## Who owns what

| State | Owner | Changed with |
|---|---|---|
| Binary | root, on the edge | `sbctl edge upgrade` |
| Host settings: listeners, TLS, identity, trusted proxies, DNS, Pages | root config file (`edge.config`) | `sbctl edge config` from a base file the operator keeps |
| Routes, projects, GitHub binding | the edge (`runtime_file`, service account) | the console (Applications) or its API |
| Site builds | the edge's Pages store | `sbctl site publish`, CI (`PAGES-004`/`PAGES-008`) or the console |
| Visitor DNS of a new name | the DNS provider | the console's Connect panel (`dns.cloudflare`) or by hand |
| Moving a live name to the edge | the DNS provider | `sbctl site cutover` |

Routes are never in the operator's files: in single-file mode every config
install would replace what the console saved, and with
`ProtectSystem=strict` the edge cannot write `/etc` anyway.

## Targets

`~/.config/sbctl/targets.json` (or `./sbctl.json`), mode 600, one entry per
edge; see [sbctl.example.json](../../sbctl.example.json). Every path is data,
so a second server is a second entry. `socket` is the admin Unix socket,
`admin` the loopback admin listener reached through an SSH tunnel, and
`sites_token` a file on the operator's machine with the edge's
`admin.sites_token`, which can publish and roll back sites and nothing else.
`lock` is shared with any other deployment tool for that edge.

## Commands

```sh
sbctl edge upgrade [-ref REF] [-apply]
```
Builds a committed ref with `git archive` and runs `switchboard upgrade` on
the edge ([systemd.md](systemd.md)).

```sh
sbctl edge config -base FILE [-cloudflare-ranges] [-apply]
```
Installs the host settings in `FILE`: the root config without routes,
projects or `github`, and with `runtime_file` (refused otherwise). The live
routes, projects and binding are carried into the candidate only as the seed
of a runtime file that does not exist yet, so adopting `runtime_file` on an
edge loses nothing; an existing runtime file is never replaced.
`-cloudflare-ranges` replaces `trusted_proxies` with Cloudflare's published
lists and prints what changed, so the firewall is updated together.

The plan names the changed fields and whether a reload is enough. With
`-apply`: the candidate is validated by the edge's binary as the SSH user;
then, as root, the staged files are hash-checked, the runtime file is seeded
if missing (owned by the service account, 600), the installed binary
validates the result with the real runtime file, the previous config is kept
as `config.bak-<stamp>`, the unit reloads or restarts, and the script waits
until the edge reports on its admin socket the digest of the files on disk.
If it does not, the previous config is restored and a runtime file the run
created is removed.

```sh
sbctl site publish -site NAME -dir DIST [-host HOST] [-apply]
```
Packs a build (regular files only; a symlink is refused), uploads it through
an SSH tunnel with the sites token, and with `-host` checks the public answer:
`/` serves this build's `index.html`, `/.env` is 404 and plain HTTP redirects
to HTTPS. A failed check rolls the site back to the build that was live
before. Building is the project's job (or CI's); the address is added once in
the console.

```sh
sbctl site cutover -host HOST -to IP -cloudflare-token FILE [-dir DIST] [-apply]
sbctl site cutover -host HOST -back EVIDENCE -cloudflare-token FILE [-apply]
```
Moves a name that something else serves (for example an apex on Cloudflare
Pages) to the edge. Its address records are replaced by a proxied A record in
one DNS batch, so the name is never left without an answer; the edge must
then answer it (an unknown path answers 404 five times in a row, which a
Pages project never does) and, with `-dir`, serve that build. A Pages custom
domain keeps the name on Pages whatever DNS says: the command says when to
remove it and waits up to ten minutes. A failure or Ctrl-C puts the previous
record back. The replaced records are kept in `~/.local/state/sbctl`;
`-back FILE` restores them later. The token needs only Zone · DNS · Edit.

## Moving an edge from single-file config to the runtime file

1. `sbctl edge upgrade -apply`: a binary with the console's Applications flow.
2. Write the base: the live config without `routes`, `projects` and `github`,
   plus `"runtime_file": "/var/lib/<unit>/runtime.json"` (inside the unit's
   `StateDirectory`), and `dns` if wanted.
3. `sbctl edge config -base FILE` (plan), then `-apply`: one restart; the
   routes in effect move to the runtime file.
4. Add or change routes in the console from then on. Retire any tool that
   still renders routes into the root config: it would switch the edge back
   to single-file mode.
