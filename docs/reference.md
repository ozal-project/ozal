# Configuration and CLI

Reference for the current checkout. The [installation](operations/download-install.md),
[first application](operations/first-application.md) and
[administration](operations/administration.md) guides contain the full walkthroughs.
For exact fields, generate the schema with your version's binary and validate the
cross-field rules; do not use an example from another release as the active configuration.

```sh
ozal schema > ozal-config.schema.json
ozal validate -config config.json
ozal doctor -config config.json -db ./state.db
ozal version -json
ozal completion bash  # also zsh, fish, powershell
```

`doctor` checks configuration and the database, not whether a public application is
reachable. `init -config FILE` creates a new private file; `setup -dir DIR` prepares a
complete local installation. Both refuse to overwrite.

## Installation and publishing in Go

```sh
ozal install binary -archive FILE.tar.gz -sha256 TRUSTED_HEX -destination NEW_FILE [-apply]
ozal install service [-binary VERIFIED_ELF | -archive FILE.tar.gz -sha256 TRUSTED_HEX] [-target URL] [-group members] [-root STAGING_DIR] [-apply]
ozal install service -print-unit
ozal uninstall [-apply] [-purge-state] [-root STAGING_DIR]

ozalctl site check -dir DIST
ozalctl site pack -dir DIST -out NEW_ARCHIVE
ozalctl site deploy -endpoint HTTPS_ORIGIN -project NAME -archive FILE -result NEW_RECEIPT [-apply]
ozalctl site accept -url HTTPS_ORIGIN -dir DIST [-attempts 10] [-wait 3s]
ozalctl cloudflare origin-pull -host HOST -like REFERENCE -token-file FILE [-backup NEW_FILE] [-apply]
ozalctl cloudflare deploy-rule -host HOST -token-file FILE [-backup NEW_FILE] [-apply]
```

`install` and `site deploy` show a plan by default. The service installer uses the
executable itself when `-binary` is omitted; it is Linux/systemd only. `ozal update`
downloads by streaming and requires the size, hash and candidate version to match the
reviewed plan. It checks the space budget per filesystem with a 64 MiB margin; an
`-apply` succeeds only if the unit runs the verified image and answers health checks.
A check without `-apply` still returns metadata only. Details on the budget, permissions
and operation are in [administration](operations/administration.md#automatic-update).

The scanner recognizes private names and known patterns; it does not prove the absence
of any secret. `site check`, `pack` and `accept` use no targets or credentials; `pack`
creates a new 0600 archive. Deploy uses the Actions variables
`ACTIONS_ID_TOKEN_REQUEST_URL` and `ACTIONS_ID_TOKEN_REQUEST_TOKEN`, only with `-apply`;
`DEPLOY_ENDPOINT` and `DEPLOY_PROJECT` are optional defaults. It neither prints nor saves
the JWT. The new 0600 receipt contains only the project, ID, live state and commit. If it
ends with `confirmed:false` or a network error, check the state before repeating: there is
no blind retry. In Actions it emits `live=true` to `GITHUB_OUTPUT` after a validated 201,
so the workflow can restore the last accepted build even if the later GET fails.
Acceptance compares an asset by SHA-256, requires 404 for private and unknown paths, and
requires an HTTP-to-HTTPS redirect on the same host. A site without assets uses its index
as proof, and therefore requires identical HTML.

## Local tunnel authority

```sh
ozal tunnel ca init -dir NEW_PRIVATE_DIR [-days 366] [-apply]
ozal tunnel identity prepare -role edge -dir NEW_PRIVATE_DIR [-apply]
ozal tunnel identity sign -role edge -ca CA_DIRECTORY -csr PUBLIC_CSR -out NEW_CERT [-apply]
ozal tunnel identity check -role edge -dir IDENTITY_DIRECTORY -ca PUBLIC_CA_FILE
```

The role may also be `connector`. Local keys are 0600, new directories are 0700, and
nothing is overwritten. Signing accepts only a bounded public CSR and fixes the SAN and
EKU to the role; it does not accept identities chosen by the CSR. The CA must be current,
correctly marked, and have more than 37 days of validity. Creating a CA does not deploy
trust, enroll connectors or automate the rollover. Use `connector` for enrollment and
`tunnel renew` for the existing operational flow. These commands do not require any
historical scripts.

```text
ozal tunnel renew -edge-ssh USER@HOST -edge-key SSH_KEY -edge-agent 'EDGE_COMMAND' -probe-file FILE [-force] [-init]
```

`tunnel renew` runs on the connector host. Its CA lives in
`~/.local/share/ozal-tunnel-ca`, the connector identities in
`~/.local/share/ozal-tunnel-connector` (user unit `ozal-tunnel-connector.service`),
and its journal and lock in `~/.local/share/ozal-tunnel-renew`. The edge is reached
over SSH with strict host keys and only `-edge-key`; `-edge-agent` is the exact
remote command, normally `sudo -n -u ACCOUNT BINARY tunnel renew-agent -role edge
-dir DIR -config CONFIG -unit UNIT`, which the edge's sudoers rule names. After a
restart the edge must serve `-probe-file` at `/` through the tunnel. All four are
required.

## Base configuration

Example of a public local HTTP proxy, without login:

```json
{
  "listen": "127.0.0.1:8888",
  "admin": {"listen": "127.0.0.1:9090"},
  "routes": [{"host": "app.localhost", "target": "http://127.0.0.1:3000"}]
}
```

```sh
ozal -config config.json
```

`config.example.json` and `config.quickstart.json` in the root contain examples; create
your own local file and do not commit it to Git. Validation rejects unknown keys.

| Field | Use |
|---|---|
| `listen` | HTTP listener; ignored when TLS uses the fixed public listeners |
| `admin.listen` | Loopback administration; default `127.0.0.1:9090` |
| `admin.socket` | Private Unix socket; its file permission grants local authority |
| `admin.socket_group` | System group with access to the socket (mode `0660`); its members operate without `sudo` and are audited as `local:<user>`. Requires `admin.socket`; rejects `root`/`wheel`, and startup fails if the service does not belong to the group or the directory is not traversable |
| `admin.login_host` | Administrator passkeys are created and used at `auth.passkeys.login_url` (HTTPS) instead of `http://ozal.localhost`, where many authenticators do not work; the console stays on loopback and receives the session through a one-time code bound to a verifier in the tab. Requires `auth.passkeys`. Earlier passkeys on `ozal.localhost` keep working. Visitors and administrators then share the login domain: the administrator sign-in names only administrator passkeys to the browser, and an administrator passkey is saved as `<email> · Ozal console` and a visitor passkey as `<email> · sign-in`, so a password manager does not offer a visitor passkey for the console. The visitor sign-in cannot narrow the list the same way without revealing which emails have an account, so a console passkey chosen there gets a message saying to pick the other one |
| `admin.update_socket` | Unix socket of the local update executor (`ozal update-executor`). If omitted, the console only reads the status report; it prepares no plans and installs nothing. The executor's root-owned profile fixes the unit, binary, configuration, catalog, key and job directory |
| `admin.token` | Reference to the full administrative credential |
| `admin.sites_token` | Reference to a limited site-publisher credential |
| `runtime_file` | Operational projection of the service: routes, projects and GitHub |
| `routes` | Hosts and destinations, with optional policy |
| `upstream_timeout_seconds` | Wait for upstream headers: 60 s by default, maximum 3600; does not limit the body |
| `dev_errors` | Local diagnostics; keep `false` on any public exposure |

The runtime is initialized explicitly and does not replace listeners, TLS or host
authority. See [service state ownership](operations/administration.md#service-and-state-ownership).

## Routes and access

Each route chooses **one** destination: `target` (URL), `upstreams` (several URLs),
`root` (absolute directory), `site` (Pages), an enrolled `connector`, or `redirect`
(absolute http(s) URL). A host without a port answers on any port; one with a port matches
only there and takes priority. Unknown hosts do not reveal the public catalog.

| Route field | Behavior |
|---|---|
| `health_path` | HTTP probe; defaults to `/`; does not probe a static route or a connector |
| `upstreams` | Selection among healthy origins, with passive failure detection |
| `spa` | For static routes, falls back unknown paths to the index |
| `redirect` | Permanent redirect for a retired hostname: GET/HEAD get 301 and everything else 308, keeping path and query (`old/docs?x=1` → `https://new/docs?x=1`); a path in the URL is a prefix. No user info, query or fragment; never to its own host; does not accept `access`, `health_path` or `spa`, and is not probed. In the console: *Add application → A redirect to another address* |
| `access` absent | Public route, except the legacy `auth_required` shorthand |
| `access: {}` | Requires an authenticated identity |
| `access.allowed_emails`, `allowed_domains` | Allowlist by **verified** email: matches are OR-ed |
| `access.required_groups` | Must have **all** groups; AND with the allowlist |
| `access.methods` | Allowed configured methods: `passkey`, `oidc` |
| `access.max_auth_age_minutes` | Recent passkey proof; step-up if exceeded |
| `access.require_device_bound` | Requires a non-syncable passkey |
| `access.context_rule` | Condition with explicit sources; unknown required evidence denies |
| `resource` | Declared owner, class, sensitivity and actions; not inferred evidence |
| `audit_mode` | `required` or `best_effort` for access when the family is enabled |

An unavailable upstream answers 502; header timeout 504; no healthy upstream 503. A
disconnected connector answers 502 and has no TCP fallback. Connectors can be protected
through `access` in the integrated process.

## Login and sessions

External OIDC configures `auth.issuer`, `client_id`, `client_secret` and `redirect_url`.
The last must match exactly the authorized callback `/_/auth/callback`. Only OIDC requires
a shared `cookie_domain` for private apps on different hosts. Login and redirects are
restricted to authorized hosts.

Native passkeys need SQLite and:

```json
"auth": {
  "passkeys": {"rp_id": "example.com", "login_url": "https://auth.example.com"},
  "cookie_domain": "example.com"
}
```

This is a fragment for a complete configuration, not a standalone file. Use HTTPS except
in the localhost development context. Passkeys are bound to the RP and origin; changing
domain requires enrollment, not copying credentials.

OIDC and passkeys can coexist: the callback is `login_url` + `/_/auth/callback`,
`provider_name` labels the button, and the visitor chooses. A recent or device-bound
step-up requires passkeys; it is not replaced by silent SSO.

Default sessions: `session_ttl_minutes: 10080` (7 days absolute) and
`session_idle_minutes: 720` (12 h without use). Use is recorded at most once per minute.
Without `-db`, sessions live in memory; with a migrated database, the session ID hashes
are persisted. Lowering the TTL limits existing sessions without extending their issued
expiry. Logout uses POST. Current groups and revocations are checked on the server.

The visitor portal is at `/_/auth/me`, and devices at `/_/auth/devices`. Private apps
outside `cookie_domain` with central passkey login use host sessions; the protocol and its
limits are in [host sessions](architecture/host-sessions.md). A code change is not the
same as HTTPS, browser or real-domain acceptance; see the host-sessions guide.

Invitations and recovery: [first application](operations/first-application.md). The
administrative identity is separate from visitor identities and has its own session
lifecycle; the times in this section are not the console's.

## Secrets

Credentials accept `env:NAME` or `file:/path/to/secret`; the source must exist and not be
empty. The prefixes are reserved. The API persists references, not resolved values. Avoid
inline secrets in real installations.

An unresolvable OIDC secret prevents startup; an unresolvable administrative token rejects
the operations that need it. Never copy tokens into curl examples, URLs or chats. Use the
host socket or CLI references.

## HTTPS and DNS

`tls.enabled: true` uses :443. Without `dns01`, autocert uses :80 for validation and
redirection; DNS must reach that server. Keep `tls.cache_dir` persistent. The HTTP-01
policy follows configured routes: adding an authorized host does not issue certificates
for any SNI and does not require restarting the route allowlist.

### Direct profile without Cloudflare

[config.direct.example.json](../config.direct.example.json) is a template for your own
public edge with passkey login and two private apps, one on another domain. Replace the
domains, email and destinations before using it. It requires no `dns.cloudflare`,
`tls.dns01`, `trusted_proxies` or Origin Pulls certificate. `listen` applies only to HTTP
mode without TLS; with TLS, :80 and :443 are used.

With any DNS provider, create A/AAAA for `edge.example.com` pointing to the edge's IP, and a
CNAME `*.example.com` pointing to that name. The DNS wildcard lets you add subdomains
without creating another record; **it is not a wildcard certificate**: HTTP-01 obtains one
leaf per configured name. The apex and existing records need their own review; a wildcard
does not replace them. For an app in another zone, its owner creates the matching
A/AAAA/CNAME pointing to the same edge. Do not publish an AAAA record if IPv6 does not reach
the server. DNS must resolve directly to the edge, with TCP 80/443 reachable and no other
process occupying them; also check CAA if your zone restricts the authorized CAs.

Install following the service guide and apply the template as host configuration, with
the installer's permissions and directories. Initialize `runtime_file` once with
`ozal runtime init -config /etc/ozal/config.json`, migrate SQLite, and start with
`-config /etc/ozal/config.json -db /var/lib/ozal/state.db`. Complete the administrative
bootstrap through the private transport and invite the permitted email. Keep the ACME and
SQLite state in backups. The console stays on loopback or the socket; the example does not
expose it over public HTTPS.

For a migration from a proxy, prepare this profile separately: do not remove the active
service's origin authentication as a fallback mechanism. Before the cutover, verify from
another DNS and TLS network: permitted and denied access, real IP, revocation and restart.
Keep the previous service until that acceptance; returning to the previous profile and DNS
is the rollback. The example does not change firewall or DNS and does not establish the
controls of [direct exposure](security.md#direct-ingress-or-behind-a-proxy).

### Cloudflare Tunnel profile

For a host without a public address, or one whose address you do not want to
publish. [config.cloudflare-tunnel.example.json](../config.cloudflare-tunnel.example.json)
is a template; `ozal install service -ingress cloudflare-tunnel` writes the same
shape (see the [service guide](operations/download-install.md#3-install-the-linux-service)).

```json
"ingress": {
  "mode": "cloudflare_tunnel",
  "cloudflare_tunnel": {
    "token": "file:/etc/ozal/cloudflare.token",
    "state_dir": "/var/lib/ozal/cloudflare-tunnel",
    "name": "ozal"
  }
}
```

| Field | Use |
|---|---|
| `ingress.mode` | `direct` (default when omitted) or `cloudflare_tunnel` |
| `cloudflare_tunnel.token` | `file:` or `env:` reference to a Cloudflare API token; an inline value is rejected |
| `cloudflare_tunnel.state_dir` | Tunnel identity and connector token; default `/var/lib/ozal/cloudflare-tunnel` |
| `cloudflare_tunnel.name` | Tunnel name in the account; default `ozal` |

The token needs *Account · Cloudflare Tunnel · Edit*, *Zone · DNS · Edit* on the
served zones and *Zone · Zone Settings · Edit* (for Always Use HTTPS). Without
`dns.cloudflare`, records are written with this token.

Validation requires `listen` on a loopback IP, `tls.enabled` false, an `https`
`auth.passkeys.login_url` (and `auth.redirect_url` with OIDC) and an empty
`dns.target`. `trusted_proxies` is set to `127.0.0.1/32` and `::1/128`; any other
entry is rejected.

Under **Domains**, **Create tunnel** finds the account from `auth.passkeys.rp_id`
(or `auth.cookie_domain`), creates a remotely managed tunnel, saves its identity
and connector token in `state_dir` and routes every hostname to
`http://<listen>`. Ozal decides which hosts it serves; a new application only
needs its DNS record. An active tunnel with the same name is reused instead of
creating a second one; a tunnel managed by a local `cloudflared` file is refused.
Then install `cloudflared` from Cloudflare's packages and start
`sudo systemctl enable --now ozal-cloudflared`. The unit runs
`cloudflared tunnel run --token-file`, so it needs a `cloudflared` release that
has that flag.

Each application's DNS panel creates a proxied CNAME to
`<tunnel-id>.cfargotunnel.com`. Cloudflare's Universal SSL covers the domain and
one level of subdomains; deeper names need an advanced certificate at Cloudflare.

### DNS-01

DNS-01 uses `tls.domains` and a `tls.dns01` block:

```json
"dns01": {
  "state_dir": "/var/lib/ozal/origin-cert",
  "directory_url": "https://acme-v02.api.letsencrypt.org/directory",
  "dns_token": "file:/etc/ozal/cloudflare-dns.token"
}
```

It goes inside `tls`; without `dns_provider` the adapter is Cloudflare. `tls.domains`
accepts up to 8 names, with one-level wildcards and an additional apex; their bases are the
initial zones (see [Zones](#zones-one-certificate-per-domain)). TLS-ALPN and HTTP are not
needed for that issuance. The state stores the account, generations and cleanup; a second
writer is rejected. Changing these options requires a restart.

#### Zones: one certificate per domain

With `tls.dns01` the edge issues **one certificate per zone**, shaped like a universal
certificate: `example.com` and `*.example.com`. Each zone has its own storage
(`_zone.<zone>` in `state_dir`), its backoff and its renewal: if one zone's challenge fails,
the others keep serving and renewing. Zones are issued one at a time, because they share the
ACME account and the provider; waiting for a turn does not count against the five-minute
limit of each attempt.

Zones have two origins:

| Origin | Definition | Change |
|---|---|---|
| `config` | The base of each `tls.domains` name (`*.example.com` and `example.com` are the zone `example.com`) | Edit the host configuration and restart |
| `runtime` | `zones` in `runtime_file`, added through the API or the console | `POST /_/api/zones` and `DELETE /_/api/zones/{zone}`, no restart |

```sh
# applications tool (token, socket or operator session).
curl -s --unix-socket /run/ozal/admin.sock http://localhost/_/api/zones
curl -s --unix-socket /run/ozal/admin.sock -X POST http://localhost/_/api/zones \
  -d '{"name": "example.org", "expected": "<config_sha256 from the listing>"}'
```

The listing returns, per zone, its origin, state (`pending`, `queued`, `issuing`, `active`,
`renewing`, `renewal_failed`, `expiring`, `failed`), expiry, last error, the applications it
covers and the DNS record to create (`challenge`). With `dns_provider: "builtin"`, a zone
without an entry in `challenge_delegation` delegates to `<zone>.<builtin.zone>`: create the
CNAME `_acme-challenge.<zone>` pointing to that name; the edge verifies it before writing
anything. With `hetzner` or `rfc2136`, a zone whose challenge falls outside the provider's
zone is rejected. Removing a `config` zone is rejected, as is a zone that is still the only
certificate of some application.

`POST /_/api/zones/{zone}/check` looks up the `_acme-challenge` CNAME of a served zone (never
of other names) and answers `ok` or `bad` with the exact record to create.
`POST /_/api/zones/{zone}/renew` requests an attempt now, without waiting for the backoff:
it answers 202, 409 if one is already running or queued, and 429 if the last one was less
than a minute ago or the CA asked to wait.

**Provider setup.** `GET /_/api/zones/{zone}/setup` returns what the zone still needs outside
the edge, as steps `challenge` (the CNAME above), `encryption` (the proxy's SSL mode),
`origin_pull` (the client certificate the proxy presents) and `https_redirect` (*Always Use
HTTPS*: the proxy redirects `http://` instead of forwarding it to the edge's port 80, which
usually does not listen behind a proxy and yields error 522). Each is `done`, `todo`,
`blocked`, `unknown` or `not_needed`, with the possible action and the manual instruction.
`POST /_/api/zones/{zone}/setup` performs the `todo` steps through the Cloudflare API: it
creates the CNAME if the name is empty, moves `off`/`flexible` to Full (strict), uploads the
edge's own certificate to Authenticated Origin Pulls at zone level and enables it, and turns
on *Always Use HTTPS* if it is off. It never deletes or replaces another party's record or
certificate (a different record is marked `blocked`), and leaves `full` as it is. It returns
the state afterwards; if a step fails, it answers 502 with `error` and `report`. It needs
`dns.cloudflare.token` with *DNS · Edit*, *Zone Settings · Edit* and *SSL and Certificates ·
Edit* over the zone; without a token, the steps are read from public DNS where possible and
the rest is described manually.

The proxy certificate is the edge's own identity in `<dns01.state_dir>/origin-identity/`: a
one-use CA signs a single client certificate (RSA, 3 years), and its key is discarded without
touching disk, so nothing on the host can issue another accepted certificate. `ca.pem` is
added to `tls.origin_client_ca_file` on the public listener without a restart; `client.pem`
and `client.key` (0600) exist only so they can be uploaded to the proxy. It is offered only
when the edge requires a client certificate (`tls.origin_client_ca_file`); a zone certificate
the edge does not trust is reported as `todo`, because Cloudflare would answer 520.

Migration: the earlier combined certificate (keyed by the first name of `tls.domains`) is
loaded only as a fallback and is no longer renewed. It answers for its names while the
corresponding zone has no certificate of its own, also after a restart; it is never
overwritten. A name outside every zone and outside the fallback fails closed. `ozal dns01 try`
and `dns01 check` still rehearse the `tls.domains` names as a single certificate.

#### Another CA: external account (EAB) and Google Trust Services

`directory_url` accepts any ACME CA. Some (Google Trust Services, ZeroSSL) do not open
anonymous accounts: they require *external account binding* (RFC 8555 §7.3.4), a key and
identifier issued beforehand on the operator's account with that CA.

```json
"dns01": {
  "state_dir": "/var/lib/ozal/origin-cert",
  "directory_url": "https://dv.acme-v02.api.pki.goog/directory",
  "external_account": {"key_id": "KEY_ID_DE_LA_CA", "hmac_key": "file:/etc/ozal/eab.key"}
}
```

`hmac_key` is always a `file:` or `env:` reference, never the value. It accepts the key in
base64url as Google delivers it (`b64MacKey`), and also the standard form with padding. It
is read only when the account is registered: the account stays in `state_dir`, and renewals
do not use it again. It is supported with any `dns_provider`.

Google Trust Services: production `https://dv.acme-v02.api.pki.goog/directory`, test
`https://dv.acme-v02.test-api.pki.goog/directory`. The key is requested with
`gcloud publicca external-account-keys create` (API `publicca.googleapis.com`, role
`roles/publicca.externalAccountKeyCreator`; `keyId` and `b64MacKey` appear in the output).
**One key registers only one account, and expires after 7 days unused.** Test keys are
requested against the preproduction endpoint (`gcloud config set
api_endpoint_overrides/publicca https://preprod-publicca.googleapis.com/`). Because
`ozal dns01 try` works in a temporary directory, **each rehearsal registers a new account and
consumes an EAB key**: request a test key per rehearsal. If the CA accepts the registration but
the local save fails, the key is spent and another must be requested. Changing CA means
changing `directory_url` (and `state_dir`, or letting the store separate accounts by
directory); there is no other shared state.

#### Independent DNS provider (RFC 2136)

To avoid depending on a provider's API, `dns_provider: "rfc2136"` creates and deletes the
challenge TXT records with TSIG-signed dynamic UPDATE against an authoritative server that
accepts updates (BIND, PowerDNS, Knot):

```json
"dns01": {
  "state_dir": "/var/lib/ozal/origin-cert",
  "directory_url": "https://acme-v02.api.letsencrypt.org/directory",
  "dns_provider": "rfc2136",
  "rfc2136": {
    "server": "ns1.example.com:53",
    "zone": "example.com",
    "key_name": "ozal-acme",
    "algorithm": "hmac-sha256",
    "secret": "file:/etc/ozal/acme-tsig.secret"
  }
}
```

`secret` is a `file:` or `env:` reference to the TSIG secret in base64 (an inline value is
rejected), and `dns_token` is not supported with this provider. The zone is explicit and must
contain every name in `tls.domains`; the adapter sends no updates for names outside it. Only
`hmac-sha256` (default), `hmac-sha384` and `hmac-sha512` are accepted. The transaction goes
over TCP; a success response without a valid signature counts as an unknown result, and
deletion removes only the record it created (exact name and value), never the whole set. On
the server, limit the key to that name with an `update-policy` rule equivalent to `grant
ozal-acme name _acme-challenge.example.com. TXT;`. This path proves issuance without
Cloudflare; it does not change who is authoritative for your zone or replace its network
protection.

#### Hetzner Cloud DNS

`dns_provider: "hetzner"` uses the Hetzner Cloud DNS API for an explicit zone:

```json
"dns01": {
  "state_dir": "/var/lib/ozal/origin-cert",
  "directory_url": "https://acme-v02.api.letsencrypt.org/directory",
  "dns_provider": "hetzner",
  "hetzner": {"zone": "example.com", "token": "file:/etc/ozal/hetzner-dns.token"}
}
```

`token` is a `file:` or `env:` reference (an inline value is rejected), and `dns_token` is
not supported. The zone must contain every name in `tls.domains`, and the adapter does not
act outside it; no permission to list zones is needed. Each change is an asynchronous Action:
the adapter waits for it to finish before treating the record as created or deleted, and an
Action that never finishes counts as an unknown result. Deletion removes only the record it
created (the API identifies them by value inside the RRSet). The TTL is fixed at 60 s, the
minimum. **Known limit:** the official API specification does not state how TXT records
must be written. Ozal sends them quoted. Test the provider against a staging zone before
relying on it for production certificates.

#### Delegating the challenge by CNAME (any DNS provider)

If your domain lives in a DNS without an adapter (a registrar such as DonDominio or Porkbun,
or any other), you do not need one. Create **once, by hand**, a CNAME, and let Ozal write the
TXT records in a zone you do control:

```
_acme-challenge.example.com.   CNAME   example-com.acme.delegated.net.
```

```json
"dns01": {
  "state_dir": "/var/lib/ozal/origin-cert",
  "directory_url": "https://acme-v02.api.letsencrypt.org/directory",
  "dns_provider": "hetzner",
  "hetzner": {"zone": "delegated.net", "token": "file:/etc/ozal/hetzner-dns.token"},
  "challenge_delegation": {"example.com": "example-com.acme.delegated.net"}
}
```

Each key is the base domain of a `tls.domains` name (a wildcard and its apex share a base),
and its value is the name where the TXT is written. The provider's zone must contain that
target, not the domain. Before creating any record, Ozal checks that `_acme-challenge.<domain>`
is a CNAME to the exact target; if not, it fails with a message naming the missing CNAME and
creates nothing. Propagation waiting queries the name the CA sees, that is, through the CNAME.
One hop only: a CNAME chain is not accepted.

**Whoever controls the target zone can obtain certificates for the domain.** Use a dedicated
zone and a credential limited to it. This decouples the certificates from the provider; it
does not change who serves your domain's DNS or who protects you from DDoS. Without a
wildcard you need none of this: HTTP-01 of the [direct profile](#direct-profile-without-cloudflare)
does not use DNS.

#### Own DNS server (`builtin`): no account or API from anyone

With `dns_provider: "builtin"`, Ozal answers itself, as an authoritative DNS server, for a
zone that contains only the challenge TXT records. There is no token, account or API: the
record is published in memory and disappears when issuance ends. It is the acme-dns pattern,
inside the same process.

```json
"dns01": {
  "state_dir": "/var/lib/ozal/origin-cert",
  "directory_url": "https://acme-v02.api.letsencrypt.org/directory",
  "dns_provider": "builtin",
  "builtin": {
    "zone": "acme.example.net",
    "listen": "203.0.113.7:53",
    "nameservers": ["ns1.example.net"]
  },
  "challenge_delegation": {"example.com": "example-com.acme.example.net"}
}
```

**Three static records** are needed, created once by hand, in the DNS that serves each domain
(any provider or registrar, without an API):

```
ns1.example.net.                 A      203.0.113.7                      ; pegamento, sin proxy
acme.example.net.                NS     ns1.example.net.                 ; delega la zona a Ozal
_acme-challenge.example.com.     CNAME  example-com.acme.example.net.    ; un CNAME por dominio
```

Without a wildcard, each `tls.domains` name needs its own `_acme-challenge.<name>` CNAME and
its entry in `challenge_delegation`; a wildcard and its apex share one. With the own server,
that CNAME remains a manual step once per name (or can be automated where the provider has an
API).

Check the chain before activating anything, with `dig`:

```
dig +short NS acme.example.net
dig @203.0.113.7 acme.example.net SOA +norecurse
dig +short CNAME _acme-challenge.example.com
```

`listen` is an explicit IP:port (`0.0.0.0` and hostnames are not accepted: on Linux a wildcard
bind conflicts with the local stub `127.0.0.53`). Port 53 requires `CAP_NET_BIND_SERVICE` or a
redirect; open **UDP and TCP** in the host's and the provider's firewall. If the port cannot be
opened, Ozal fails at startup rather than running with a certificate that never validates.

Server contract: it answers only for its zone (TXT, SOA and NS) and replies `REFUSED` to
everything else, without recursion; `ANY` and unsupported types return "no data", so a forged
origin gets no amplification; it never responds to a message that is itself a response;
it truncates over UDP anything over 512 bytes; it limits 20 queries per second per source and
drops the excess without answering; it serves up to 8 queries per TCP connection with a 5 s
deadline and 64 simultaneous connections. A `tls.domains` domain without an entry in
`challenge_delegation` is rejected when the configuration loads, because this server cannot
write outside its zone. Keep the zone dedicated: **whoever controls it can issue certificates
for the domains that delegate to it.**

##### Verificación previa: `ozal dns01 check`

```
ozal dns01 check -config config.json [-resolver 1.1.1.1:53]
```

It issues nothing and writes no state. Before spending an attempt on the CA, it checks that each
`_acme-challenge.<domain>` is a CNAME to the `challenge_delegation` target (if one is missing, it
prints the exact record to create), that each `builtin.nameservers` entry resolves to the
`builtin.listen` IP (the parent's glue), and that UDP and TCP on `listen` can be opened. A busy
port is a warning, not a failure: it is normal while the edge is running. **It cannot check that
port 53 is reachable from the Internet** (host or cloud firewall): it says so in a warning and
suggests the `dig` to test it from outside. It exits with an error only if something fails.
`-resolver` queries a specific DNS server instead of the system's, useful when the local one has
cached a negative answer.

##### Rehearsal without touching production: `ozal dns01 try`

```
ozal dns01 try -config dns01-try.json
```

It issues **one** certificate with the configured provider, over the real Internet, and discards
it. It does not open 443, does not use or modify `tls.dns01.state_dir` (it works in a temporary
directory that it deletes afterwards), and prints no keys or certificates. It rejects a production
CA (a `directory_url` without "staging") unless `-allow-production` is given, so as not to consume
its limits. With `builtin` it starts the DNS server on `listen`, so it needs permission for that
port (`sudo` or `CAP_NET_BIND_SERVICE`) and the port must not be busy. It prints the provider, the
names, the issuer, the expiry and whether the chain was verified: without
`certificate_roots_file` it says **"chain NOT verified"** instead of asserting it. Use a copy of
the configuration with a different `tls.dns01` pointing to the staging CA; it does not replace
acceptance of the real certificate on the edge.

This decouples certificate issuance from any DNS provider; it does not change who serves
your domain's DNS, who your registrar is, or who protects you from DDoS, and the glue and the
`NS` delegation still live in the parent zone.

Staging uses a different folder and explicit roots through `certificate_roots_file`, which
replace the validation roots for that issuance; do not use it to weaken production. Renewal
keeps the valid certificate, and without one TLS fails.

`tls.origin_client_ca_file` requires a client certificate from the authorized CA on the public
listener: it is proxy trust, separate from server TLS and from the tunnel.
[Deployment conditions](security.md#direct-ingress-or-behind-a-proxy).

For the connection wizard, `dns.target` indicates an IP or public hostname, `dns.proxied` the
record's mode, and the optional `dns.cloudflare.token` enables automatic creation and zone setup
in Cloudflare (see above). Without a token the manual record is shown. A record that might serve
an existing site is never silently replaced. `POST /_/api/apps/{host}/dns` also creates the record of
the sign-in host in `login_url`, which is not an application route.

An application can carry a display name (1 to 60 characters) that the console and its
invitations show beside the address: `PUT /_/api/apps/{host}/name` with `name` (empty clears
it), under the applications tool. The name is kept in the state database, not in the
configuration, and it is removed with the route. It never affects an access decision.

## Visitor IP

`trusted_proxies` contains literal IPs/CIDRs of trusted peers. Empty means headers are not
trusted. Only a valid `CF-Connecting-IP` from those peers is accepted; repeated, ambiguous,
non-unicast or scoped values are discarded. Never add a visitor range or a catch-all for
convenience.

Trust strips IP and identity headers and their aliases; the proxy rebuilds `X-Forwarded-For`
from the selected IP and does not forward the session cookie. `RemoteAddr` remains the transport
peer. A trusted loopback grants that authority to local processes; it does not authenticate
cloudflared as a process.

## Rate limiting and Telegram

Without `rate_limit` no buckets are configured. Example:

```json
"rate_limit": {
  "general": {"rate": 20, "burst": 40},
  "auth": {"rate": 1, "burst": 3},
  "identity": {"rate": 10, "burst": 30},
  "max_keys": 10000
}
```

Rate is requests per second; keys include the host and the trusted IP. Identity is charged
after the policy allows. Key capacity is bounded; when full, new keys are rejected. A 429 with
`Retry-After` is returned; buckets survive a reload if the configuration does not change. This is
not network-volumetric protection.

`notify.telegram` accepts a referenced `token`, a private `chats_file` and an optional HTTPS
`api_base` (HTTP only on loopback). Pair from the host with `ozal notify pair -config config.json`
and send `/pair CODE` to the bot in the chosen chat. The process is the only reader of updates;
the code is single-use and expires. The queue is bounded, retries are limited, and the request
path is never blocked; alerts can be lost. Approving access through Telegram is a separate
function.

## Pages

`pages.dir` holds artifacts, and `keep` controls retention. A `root` route serves fixed files;
`site` uses the active Pages deployment. Upload tar.gz archives of regular `dist` files from the
console, the CLI or CI. Ozal does not build.

Default extractor limits: 100 MiB per file, 500 MiB total, 20,000 entries and 32 levels. It rejects
links, traversal, devices and secret or repository files. Checking for secret content also belongs to
the build; validating names does not prove the absence of secrets in any file.

`_headers` and `_redirects` are validated on publish and are never served. Syntax example, in separate
files:

```text
# _redirects
/old /new 301
/api/* https://backend.example.com/:splat 302
# _headers
/assets/*
  Cache-Control: public, max-age=31536000, immutable
```

Redirects 301/302/303/307/308 are supported; 200 rewrites and 404/410 rules are not. File rules
cannot be used to bypass policies. A rollback activates retained content and does not change the
database, DNS or backend. CI needs a project and a dedicated deploy hostname;
see [configuration and disconnection](operations/administration.md#github-and-publishing).

`POST /_/api/projects/{name}/workflow` requires the `publishing` tool. By default it returns a plan
with no writes: `directory` (e.g. `.`), `output` (e.g. `dist`), `hostname` (one public hostname of the
project; empty picks the first) and `page` (installation repository page; default 1). The response
includes `plan.content`, `plan.digest`, `plan.branch`, `plan.path` and `expected` (the configuration
digest). Repeat the same fields with `apply: true`, `digest: plan.digest` and `expected` to propose the
PR. A change in content or configuration requires a new review. A 201 returns `pull_request` with
`url`, `number` and `branch`; write errors include the proposal branch so it can be reconciled on
GitHub. Never send App keys or tokens to the browser.

Connecting GitHub from the console (`publishing` tool, requires `-db`): `POST /_/api/github/manifest`
with `origin` (scheme and host of the console, no path) and optional `organization` returns `url` and
`manifest` for the browser to send to GitHub; the `state` is bound to that session for one hour.
`POST /_/api/github/manifest/conversion` with `code`, `state` and `expected` exchanges the code once,
writes `github-app-ID.pem` (0600, never overwriting) next to the database and saves `github` with
`installation_id: 0`; it returns `slug`, `owner` and `install_url`. `POST /_/api/github/installation`
with `expected` reads the App's single installation from GitHub and records it; it does not accept an
ID from the client. `GET /_/api/github/repositories` also reports `installed`, `connect_available`,
`workflow_available` and `install_url` when the App still needs installing. `POST /_/api/github/workflow`
previews, without saving, the workflow of a new site (`project`, `repository_id`, `page`, `ref`,
`workflow`, `hostname`, `directory`, `output`) with the same generator used for the later PR. The console's
CSP allows `form-action https://github.com` only for that manifest.

## Administration and audit

`/_/healthz` answers without a credential and does not describe routes. Status (`/_/status`,
`/_/api/status`), activity and APIs require authority. The console is private; mutations receive a
bearer with the tool or the socket's authority. The CLI uses `admin.socket` by default; `-admin` can
choose an explicit transport. `-db` on identity commands is an offline emergency, not routine
operation.

SIGHUP reloads the valid state; listener, identity and TLS changes require a restart. An error keeps
the previous snapshot. Operational edits compare revision and digest so they do not overwrite concurrent
changes.

`GET /_/api/access` answers who reached which protected application and why: a `summary` over every
decision since the history starts (`source` is `journal` when access is audited, so it survives restarts,
or `memory` since the edge started), and the newest held `entries`. Filters: `host`, `actor`, `outcome`
(`allowed`, `refused`, `step_up`, `rate_limited`), `before` (an entry's `seq`, to page back) and `limit`
(1–200, default 50); an unusable value is a 400. Any operator session may read it.

`ozal project trust -project NAME -repository-id ID -owner-id ID -ref REF -workflow-ref WORKFLOW` shows
the migration plan of an existing project's GitHub publisher. `-apply -backup FILE` first keeps the
previous trust metadata (a new file, mode 0600) and confirms the active state. It requires `admin.socket`
or `-admin unix:/path`: TCP and web tokens are rejected. The endpoint `PUT /_/api/projects/{name}/github-trust`
is socket-only; its body carries `trust`, `expected` and `apply` (false by default). It neither uses nor
replaces GitHub App exploration. The remote variant is `ozalctl project trust`; see
[remote operation](operations/administration.md#remote-operation-with-ozalctl).

`ozal project rename -project NAME -to NEW_NAME [-apply]` renames a project and its site together. The plan
names the previous site, the new one and the addresses that move; with `-apply`, the edge first copies the
live version to the new site (going through the same validation as an upload), then changes project, site
and routes in a single change with the reviewed digest, so no address is left without content. The new name
cannot be used by another project or by a route. The previous site's versions are retained: renaming back
undoes it. Afterwards, CI must publish with `DEPLOY_PROJECT` equal to the new name. It is socket-only
(`POST /_/api/projects/{name}/rename` with `to`, `expected` and `apply`); the remote variant is
`ozalctl project rename`.

`audit` has `dir`, `include` (`access`, `admin`), `admin_mode` and `max_segment_bytes`. For `access`,
private routes default to required and public ones to best_effort; a route's `audit_mode` allows an
explicit choice. For admin, required is the default when the family is included. A durable intent is not
proof of commit. [Audit contract](engineering.md#audit).

```sh
ozal audit verify -config config.json
ozal audit keygen -h
ozal audit checkpoint -h
ozal audit repair -dir /path/audit  # plan; -apply after review
```

Stop the writer before verify, checkpoint or repair. The journal is not repaired at startup and offers
no complete retention or pruning. Copy checkpoints and the pinned public key to an independent location;
the tool does not prove that custody.

## Migrations and development tools

Use the binary of the right version with a migrated database; migrations are not applied silently at
startup:

```sh
ozal migrate -db ./state.db status
ozal migrate up -db ./state.db -backup
scripts/new-migration.sh name
```

The parent folder must exist. Applied migrations are immutable, numbering is continuous, and the runner
owns transactions: do not add BEGIN/COMMIT to the SQL. Do not limit the table list to an older release's;
the schema and its embedded files are the authority. [Verification](engineering.md#verification).
