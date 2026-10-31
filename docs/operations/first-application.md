# First application and an existing website

This guide covers the current checkout: the private console, applications and
access. Each release's notes state what a distributed binary includes.

## Try it with an application that is already running

With a binary built from this checkout:

```sh
ozal setup -dir ./my-edge -target http://127.0.0.1:3000
```

This creates a **new** installation: configuration, a private token referenced
from a file, a migrated SQLite database, a Pages directory and `START-HERE.md`.
It refuses to overwrite an existing directory. The public listener for this trial
is `127.0.0.1:8888`; the route `localhost:8888` requires a passkey and the
`members` group. It does not install a service and does not change DNS or a site
that is already running.

The command prints the paths and the exact start, invitation and operator sign-in
commands. Use those paths, and do not paste credentials into a chat.

```sh
ozal -config /path/to/my-edge/config.json -db /path/to/my-edge/state.db
# In another terminal on the same host:
ozal console enrol -config /path/to/my-edge/config.json
ozal user invite -config /path/to/my-edge/config.json \
  -email ana@example.com -group members -return-to http://localhost:8888/
```

The first link opens a private administrative session; the second is a visitor
invitation. They are different credentials. Send the invitation to its recipient
over a trusted channel. With a second browser profile, check that a person
without `members` is refused. Being able to sign in does not mean that person can
administer Ozal.

For a static site, use `setup -dir ./my-site -site portfolio`. The configuration
already has persistence and Pages. The first publication is still missing: upload
a tar.gz of the `dist` contents with **Applications → More → Publish a new version**
(or when adding it with **Add application**), or use `site deploy` with the token
through its file reference. The project website is built outside the edge.

On Windows there is no administrative Unix socket; operator sessions issued by the
host require Linux or macOS. For the local Windows console, the token is kept in a
file, and offline recovery is done with the process stopped. The service
installation described in this repository is Linux/systemd only.

## First administrator and per-person access

The host opens the door once. After that, each administrator signs in with their
own passkey, and no command on the host is needed.

```sh
sudo ozal console enrol            # service installation; over SSH it also prints the `ssh -N -L` to open the link
OZAL_CONFIG=/path/config.json ozal console enrol   # configuration outside /etc/ozal
ozal console enrol -config /path/to/my-edge/config.json   # manual start
```

It prints a link that works once for ten minutes. Open it in the browser that has
the tunnel. It asks for a full name and an email address (no message is sent) and
creates the passkey. That person becomes the first administrator, with every tool.
From then on the console offers "Sign in with your passkey". A passkey is bound to
the site's name, and WebAuthn does not accept IP addresses: open the console as
`http://ozal.localhost:PORT` (the local end of the tunnel), never as `127.0.0.1`.

`ozal.localhost` resolves to the machine from the browser; it needs no public DNS
record and no change to the SSH tunnel or the listener port. If another tool uses
the system DNS and does not resolve the name, you can add `127.0.0.1 ozal.localhost`
to your machine's `hosts` file. The browser allows passkeys in this local context
without installing a TLS certificate.

**Where to store the passkey.** Before the browser dialog, the console and the
login pages ask where it will be kept: this device, a password manager with a
browser extension (for example Bitwarden or self-hosted Vaultwarden, 1Password,
Proton Pass or KeePassXC; none is required), a phone by QR code, or a FIDO2 key.
They recommend one based on what the browser reports, and pass the choice as a
*hint*, which some browsers ignore. On a Linux desktop without an authenticator,
and in a VM without Bluetooth, a password manager or a key works. In the console
(`http://ozal.localhost`), many managers, Bitwarden among them, do not offer
passkeys; KeePassXC does, with *Settings → Browser Integration → Advanced → Allow
using localhost with passkeys*. On the HTTPS login pages this limitation does not
apply, although each combination of browser, extension and operating system can
differ. Share `https://<login host>/_/auth/help` with the people you invite: it
compares the options without requiring a session, and suggest a second passkey in
another place. If the tunnel uses a local port other than `admin.listen`, add
`-url http://ozal.localhost:PORT` to `console enrol` and `console login` so the link
opens in that browser.

**Console passkeys on the domain.** With `admin.login_host: true` and passkey login
over HTTPS, "Create my passkey" and "Sign in with passkey" go to
`https://<login host>/_/auth/admin`, where the localhost limitation for managers,
phones and keys does not apply. They return to the console with the session open.
The console stays private: without a transaction started from it, that address
answers like an unknown route. Without an HTTPS domain, first access still uses
`ozal.localhost` or the login host.

**Moving from localhost:** passkeys are bound to their name and are not copied to
the new domain. Sign in at `http://localhost:PORT`, open your menu and choose
**Sign in on another browser**. The link already uses `ozal.localhost`: open it and
create a passkey for the same account. Earlier passkeys keep working on
`localhost`. If you can no longer use them, use the host recovery, which does
replace the earlier ones.

If you lose your device, the host issues a recovery:
`sudo ozal console recover -email you@example.com`. It revokes that person's
earlier passkeys and creates a new one through the link. A second `console enrol`
is refused while an administrator exists. Administrators are not application
users: a visitor's passkey does not open the console.

From the console, **Administrators** lets you invite another person by email with a
subset of your tools (never more than you have), and disable them; their open
sessions end immediately. The invitation works once for 24 hours, and the person
opens it as `ozal.localhost` through their own SSH tunnel.

`console login` and the admin token remain available as an emergency path and for
automation.

## Operator session

`console login` takes a name that the host asserts; it does not verify a civil
identity or create a visitor account. Authority still belongs to whoever can reach
the host's administrative socket.

**Tool permissions.** The host decides what each session may change with `-tools`
(a comma-separated list of `applications`, `people`, `sites`, `publishing`,
`connectors`; without `-tools` it grants all of them). For example,
`console login -name Ana -tools people` opens a session that can invite and edit
groups but gets 403 when changing applications or sites. An unknown name is
rejected. The console shows in **Admin** what the session includes and explains a
page it cannot open. The admin token and the host socket remain the complete host
credential and are not limited.

- The link works once for five minutes. Its fragment is removed from the URL when
  it is opened and exchanged for a fifteen-minute credential.
- The active credential lives in the memory of that tab. Reloading the page does
  not end the session: the browser keeps the same credential in an `HttpOnly`,
  `SameSite=Strict` cookie that only `POST /_/api/operators/resume` receives. That
  endpoint requires the console's own origin and does not extend the session.
  Signing out, revoking, expiry or restarting the process ends it.
- `console list` shows IDs, names and expiry times; `console revoke -id ID` revokes
  a pending link or its active session, keeping the same ID.
- Mutations carry the actor `operator:<name>`. Issue, access, exit and revocation
  are recorded. When administrative audit is mandatory, its intents must be
  persisted before issuing or revoking.
- A browser session does not recover accounts, does not configure the key of a
  GitHub App, and does not enrol, rotate or revoke connectors: those actions use the
  socket.
- The global token remains available for compatibility; **Lock** removes its copy
  from this tab, and does not revoke that token in its source file.

For a VPS, prepare a private transport, for example:

```sh
ssh -N -L 19090:127.0.0.1:9090 operator@your-edge
# In the terminal on the edge, with authority over its socket:
ozal console login -config /etc/ozal/config.json \
  -name Octavio -url http://127.0.0.1:19090
```

Open the link in the browser of the machine that has the forward. Do not publish
the administrative listener on the Internet or make it the upstream of a public
route. The bootstrap does not make the first visitor the owner.

## Add a website from Applications

**Add application** is a single guided path in the console:

1. **What serves it.** An app that is already running (the private URL Ozal uses,
   for example `http://127.0.0.1:3000`), a static site (the `dist` `tar.gz`, with
   the packing command shown), a site that publishes from GitHub, or an app on
   another machine behind an already enrolled connector (chosen from the list).
   The site name comes from the address and can be changed. Ozal does not build
   anything. When editing an existing application, this step lets you keep its
   destination; publishing a new version is not done here but from **More**.
2. **Address.** With managed domains (`tls.dns01`), you type the subdomain and pick
   the domain; the console shows whether its certificate covers it. An address
   outside the managed domains requires adding the domain first under **Domains**.
3. **Who gets in.** A diagram explains the access layer (visit → passkey → rule →
   the app receives the person as `X-Auth-Request-Email` and
   `X-Auth-Request-User`). You choose a group, any person with an account, or
   public access. Several groups mean **all**. Optionally, you invite email
   addresses: each person receives a one-time link to create their passkey, and is
   added to those groups.
4. **Review.** It shows destination, access, invitations and HTTPS. Nothing changes
   until you save and confirm; the confirmation rechecks the digest.

After saving, the build is published if one was uploaded, the route is stored, the
invitations are created (their links are copied there, once only), and the panel
shows the **single DNS record** still missing, checking it every few seconds: DNS
→ certificate → a public request with a one-time token that only this edge answers
(`/.well-known/ozal-probe/…`, before the pipeline). For GitHub, the panel shows the
PR to review and waits for the first publication; if the edge cannot open PRs, it
says the workflow is added separately and continues in Sites. The list shows which
repository and branch publishes each site, and **Integrations** groups the GitHub
connection. Each row has **Edit** and a **More** menu with *DNS & HTTPS*, *Publish a
new version* and *Versions & rollback* (sites), *Who opened it* and *Remove*.

If the request reaches another server, DNS still points to the previous host. If
Cloudflare's proxy answers 52x, it could not reach the origin: check Origin Pulls
and the SSL mode. Always test one permitted person and one unpermitted person before
moving a domain in use, and block the old origin: while it stays open, **the new
policy can still be bypassed**.

### What the configuration needs

- **Certificates.** With HTTP-01 TLS (without `tls.dns01`), any saved route obtains
  its certificate on the first visit, without editing `tls.domains` or restarting;
  the listener only requests certificates for configured routes. With `tls.dns01`,
  each domain under **Domains** has its own certificate (the domain and one level of
  subdomains). They are added from the console without a restart, and a hostname
  outside them is rejected on save ([zones](../reference.md#zones-one-certificate-per-domain)).
- **DNS.** `dns.target` is the edge's public address: an IP (A or AAAA record) or a
  hostname (CNAME). Without it the panel still verifies, but cannot write the exact
  record. `dns.proxied` marks the record as proxied.
- **Cloudflare (optional).** `dns.cloudflare.token` (`file:` or `env:`) with
  *Zone · DNS · Edit* permission over the served zones enables **Create it in
  Cloudflare**. It never replaces an existing record (it may be a live site); if one
  exists, change it by hand. Without a token, the record is copied and created at any
  provider.

```json
"dns": { "target": "203.0.113.10", "proxied": true,
         "cloudflare": { "token": "file:/etc/ozal/cloudflare-dns.token" } }
```

- **A new domain behind Cloudflare.** Under **Domains → Add domain**, the card shows
  what is missing in Cloudflare: the record for certificates, the Full (strict) SSL
  mode, and the certificate the proxy presents to the edge (*Authenticated Origin
  Pulls*, if the edge uses `tls.origin_client_ca_file`; without it Cloudflare answers
  520), and *Always Use HTTPS* (without it, someone typing `http://` gets 522). With
  `dns.cloudflare.token` (permissions *DNS · Edit*, *Zone Settings · Edit* and *SSL
  and Certificates · Edit*), the **Set up in Cloudflare** button does all four: it
  uploads the edge's own certificate at zone level, so every future address on the
  domain works without another association. Without a token, each step says what to
  change in the Cloudflare dashboard. Nothing is deleted or replaced.
- **Private apps on another domain.** With passkey login, a private app outside
  `auth.cookie_domain` receives its own host session after the central login (a
  one-time code; see [host sessions](../architecture/host-sessions.md)). With external
  OIDC only, it is still rejected on review.

**Edit** keeps advanced fields when you choose to keep the destination or the
policy. Explicitly changing the basic policy replaces it: review first what access
is being removed or granted.

## Domain, rollback and backend

For an already configured Pages project, adding a hostname with the same `site`
associates it with the project when the snapshot is saved. It needs no DNS token
from a provider. The system does not let you change a project's last hostname to a
different destination and leave it orphaned. Changes persist across restarts.

If the new route was a trial, you can remove it under Applications, or restore the
previous configuration and reload. If it was a project domain, the association is
removed atomically when the route is removed; if it is the last domain, first
disconnect its publisher under CI projects. References are validated.
A Pages rollback activates a retained artifact; it does **not** revert DNS, a
database or a backend change.

Ozal does not start or install your application. If the application already has a
login, keep that login until you choose and test an
[integration](../engineering.md#identidad-y-aplicaciones) contract. Do not disable
internal permissions to save a screen.

## People, groups and sessions

People lets you assign groups when inviting and edit native membership, with a
review of the consequences. It updates only from a current read; a concurrent change
returns a conflict. When groups change, that person's native sessions are deleted in
the same transaction, and each request checks the current groups to cover a
concurrent sign-in with the removal. Re-inviting does not change memberships. The
API group catalog (`GET /_/api/groups`) combines local memberships and route
requirements; OIDC groups are managed by their provider.

At `/_/auth/me`, a visitor sees the applications they may open and their own
sessions, with last-use and expiry dates and the method. They can sign out one or all
of them; they cannot see or revoke another person's. The dates are not the device's
posture or location. The device and passkey list stays at `/_/auth/devices` and asks
for recent proof.

## Upgrade an earlier installation

Migration **012** adds connector expiry metadata and a session index. It also ends
earlier OIDC sessions: their raw `sub` values cannot be safely converted into
issuer-qualified subjects. New OIDC subjects encode the issuer and `sub`; accounts
are not linked by email. Native accounts and passkeys are kept. Native sessions
recorded with the `passkey` method remain valid; an older session with no recorded
method requires a new login.

Use the [upgrade and backup procedure](administration.md#servicio-y-propiedad-del-estado)
and apply migrations explicitly. Do not run a new version against an unmigrated
database. Reverting the binary after a migration also requires the earlier copy of
the database.
