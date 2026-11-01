# Publish your first application

This guide takes you from a running application to a private, protected address,
step by step. It covers the current checkout; each release's notes state what a
distributed binary includes.

Read the steps in order. Each one ends with a check you can run yourself.

| Step | What you do | You end up with |
|---|---|---|
| 1 | [Try it locally](#1-try-it-locally) | A new private installation on your machine |
| 2 | [Create the first administrator](#2-create-the-first-administrator) | Your passkey in the console |
| 3 | [Invite a person](#3-invite-a-person) | A protected address someone else can open |
| 4 | [Publish an application or a site](#4-publish-an-application-or-a-site) | Your app on its own address, through Ozal |
| 5 | [Point a domain at Ozal](#5-point-a-domain-at-ozal) | A public HTTPS address |

<a id="1-try-it-locally"></a>

## 1. Try it locally

With a binary built from this checkout, run an application that is already listening
on port 3000 and create a new installation:

```sh
ozal setup -dir ./my-edge -target http://127.0.0.1:3000
```

This creates configuration, a private token kept in a file, a migrated SQLite
database, a Pages directory and `START-HERE.md`. It refuses to overwrite an existing
directory. The trial listener is `127.0.0.1:8888`; the route `localhost:8888`
requires a passkey and the `members` group.

It does not install a service, change DNS or touch a site that is already running.
It prints the exact start, invitation and sign-in commands, and you should use those
paths. Do not paste credentials into a chat.

Start Ozal and keep it running:

```sh
ozal -config /path/to/my-edge/config.json -db /path/to/my-edge/state.db
```

**Check:** `curl -s http://127.0.0.1:8888/` answers, and the route for
`localhost:8888` asks you to sign in.

On Windows there is no administrative Unix socket, and operator sessions issued by
the host require Linux or macOS. The service installation is Linux/systemd only.

<a id="2-create-the-first-administrator"></a>

## 2. Create the first administrator

The host opens the door once. After that, each administrator signs in with their own
passkey, and no command on the host is needed.

In a second terminal on the same host:

```sh
ozal console enrol -config /path/to/my-edge/config.json
```

On a service installation, use `sudo ozal console enrol`. Over SSH, the command also
prints the `ssh -N -L` line you need to open the link from your own machine. With a
configuration outside `/etc/ozal`, set `OZAL_CONFIG=/path/config.json`.

The command prints a link that works once, for ten minutes. Open it in the browser
that reaches the console. You enter your full name and an email address (no message
is sent) and create the passkey. That person becomes the first administrator, with
every tool.

Open the console as `http://ozal.localhost:PORT`, where `PORT` is the local end of
your tunnel. Do not use `127.0.0.1`: WebAuthn does not accept IP addresses. The name
resolves to your machine without a public DNS record. If your system's DNS does not
resolve it, add `127.0.0.1 ozal.localhost` to your `hosts` file.

**Check:** after creating the passkey, the console shows you as signed in, and
"Sign in with your passkey" works on the next visit.

### Where to store the passkey

Before the browser dialog, the console asks where the passkey will be kept: this
device, a password manager with a browser extension, a phone by QR code, or a FIDO2
key. None of them is required. The choice is a hint, and some browsers ignore it.

- On a desktop Linux without an authenticator, and in a VM without Bluetooth, a
  password manager or a security key works.
- In the console (`http://ozal.localhost`), many password managers do not offer
  passkeys. KeePassXC does, once you enable *Settings → Browser Integration →
  Advanced → Allow using localhost with passkeys*.
- On the HTTPS login pages, that limitation does not apply. Each combination of
  browser, extension and system can still differ.

Share `https://<login host>/_/auth/help` with the people you invite. It compares the
options without requiring a session. Suggest a second passkey kept somewhere else.

### Console passkeys on your domain

With `admin.login_host: true` and passkey login over HTTPS, **Create my passkey** and
**Sign in with passkey** go to `https://<login host>/_/auth/admin`. There the
localhost limits for managers, phones and keys do not apply, and you return to the
console with your session open. The console stays private: without a transaction
started from it, that address answers like an unknown route.

### Other ways in

- **Moving from localhost.** Passkeys are bound to their name and are not copied to a
  new domain. Sign in at `http://localhost:PORT`, open your menu and choose **Sign in
  on another browser**. Open the link it gives you on `ozal.localhost` and create a
  passkey for the same account. Your earlier passkeys keep working on `localhost`.
- **Lost device.** The host issues a recovery with
  `sudo ozal console recover -email you@example.com`. It revokes that person's earlier
  passkeys and creates a new one through a link. A second `console enrol` is refused
  while an administrator exists.
- **Administrators.** From the console, **Administrators** invites another person by
  email with a subset of your tools (never more than you have). The invitation works
  once, for 24 hours. Administrators are not application users: a visitor's passkey
  does not open the console.
- **Emergency path.** `console login` and the admin token remain for emergencies and
  automation. See [operator session](#operator-session) below.

<a id="3-invite-a-person"></a>

## 3. Invite a person

Create a visitor account for someone who should reach the application. Use the group
that will grant access:

```sh
ozal user invite -config /path/to/my-edge/config.json \
  -email ana@example.com -group members -return-to http://localhost:8888/
```

The invitation is a visitor credential. It is separate from the administrator link in
step 2. Send it to its recipient over a trusted channel.

**Check:** with a second browser profile, a person who is not in `members` is refused,
and Ana can sign in and reach the page. Being able to sign in does not make someone an
administrator of Ozal.

<a id="4-publish-an-application-or-a-site"></a>

## 4. Publish an application or a site

### Applications

In the console, choose **Applications → Add application**. It is a four-step path:

1. **What serves it.** An app that is already running (the private URL Ozal uses,
   for example `http://127.0.0.1:3000`), a static site, a site that publishes from
   GitHub, or an app on another machine behind an enrolled connector. Ozal does not
   build anything. The site name comes from the address and can be changed.
2. **Address.** With managed domains (`tls.dns01`), type the subdomain and pick the
   domain; the console shows whether its certificate covers it. A domain that is not
   managed yet has to be added first under **Domains**.
3. **Who gets in.** You choose a group, any signed-in person, or public access.
   Several groups mean **all** of them. You can also invite email addresses now; each
   person receives a one-time link to create their passkey and is added to those
   groups. A short diagram shows the path: visit → passkey → rule → the app receives
   the person as `X-Auth-Request-Email` and `X-Auth-Request-User`.
4. **Review.** It shows the destination, access, invitations and HTTPS. Nothing
   changes until you save and confirm; the confirmation rechecks the digest.

After saving, the route is stored and the invitations are created. Their links are
copied there, once only. The panel then shows the single DNS record still missing and
checks it every few seconds: DNS → certificate → a public request with a one-time
token that only this edge answers.

Each row in **Applications** has **Edit** and a **More** menu, with *DNS & HTTPS*,
*Publish a new version* and *Versions & rollback* for sites, *Who opened it* and
*Remove*.

### Static sites

For a static site, start with `ozal setup -dir ./my-site -site portfolio`, or add a
site to an existing installation. The first publication uploads a `tar.gz` of your
`dist` directory: use **Applications → More → Publish a new version**, or
`site deploy` with the token from a file. Ozal does not build the site; build it
outside the edge.

To publish from GitHub, connect the repository in **Integrations → GitHub → Connect
GitHub**. The full setup is in the [administration guide](administration.md#github-and-publishing).

**Check:** the address serves your page, and a person who is not permitted gets a
refusal.

### Editing and removing

**Edit** keeps the advanced fields when you choose to keep the destination or the
policy. Changing the basic policy replaces it, so review first what access is being
removed or granted. If a route was only a trial, remove it under **Applications**, or
restore the previous configuration and reload.

<a id="5-point-a-domain-at-ozal"></a>

## 5. Point a domain at Ozal

This step makes the address public. Test the steps above first.

- **Certificates.** With HTTP-01 (no `tls.dns01`), a saved route obtains its
  certificate on the first visit. No restart is needed. With `tls.dns01`, each domain
  under **Domains** has its own certificate that covers the domain and one level of
  subdomains. A hostname outside those domains is rejected on save. See
  [zones](../reference.md#zones-one-certificate-per-domain).
- **DNS.** `dns.target` is the edge's public address: an IP (A or AAAA record) or a
  hostname (CNAME). Without it, the panel still verifies the record but cannot write
  it for you. `dns.proxied` marks the record as proxied.
- **Cloudflare (optional).** With `dns.cloudflare.token` (`file:` or `env:`) that has
  *Zone · DNS · Edit* over the served zones, **Create it in Cloudflare** writes the
  record. It never replaces an existing record, because that record may be a live site.
  Without a token, the record is copied and created at any provider.

```json
"dns": { "target": "203.0.113.10", "proxied": true,
         "cloudflare": { "token": "file:/etc/ozal/cloudflare-dns.token" } }
```

### Behind Cloudflare

Under **Domains → Add domain**, the card lists what Cloudflare still needs:

- the record for certificates;
- the Full (strict) SSL mode;
- the certificate the proxy presents to the edge (*Authenticated Origin Pulls*). This
  is needed when the edge uses `tls.origin_client_ca_file`; without it Cloudflare
  answers 520;
- *Always Use HTTPS*. Without it, someone who types `http://` gets 522.

With `dns.cloudflare.token` (*DNS · Edit*, *Zone Settings · Edit* and *SSL and
Certificates · Edit*), **Set up in Cloudflare** does all four. It uploads the edge's
certificate at zone level, so every future address on the domain works without another
association. Without a token, each step says what to change in the Cloudflare
dashboard. Nothing is deleted or replaced.

If a request reaches another server, DNS still points to the previous host. If
Cloudflare answers 52x, it could not reach the origin: check Origin Pulls and the SSL
mode.

**Before you move a domain that is in use**, test one permitted person and one
unpermitted person, and block the old origin. While the old origin stays open,
**the new policy can still be bypassed**.

### Private apps on another domain

With passkey login, a private app outside `auth.cookie_domain` receives its own host
session after the central login, using a one-time code. See
[host sessions](../architecture/host-sessions.md). With external OIDC only, that app is
rejected when you review it.

<a id="what-ozal-does-not-do"></a>

## What Ozal does not do

- It does not start or install your application.
- It does not replace your application's permissions. If the app already has a login,
  keep it until you choose and test an
  [integration](../engineering.md#identity-and-applications) contract.
- It does not revert DNS, a database or a backend change when you roll back a site. A
  Pages rollback activates a retained artifact only.

## Operator session

These are the console's operator sessions, for administrators who need limited access.

`console login` takes a name that the host asserts. It does not verify a civil identity
or create a visitor account; authority still belongs to whoever can reach the host's
administrative socket.

**Tool permissions.** The host decides what each session may change with `-tools`, a
comma-separated list of `applications`, `people`, `sites`, `publishing` and
`connectors`. Without `-tools`, it grants all of them. For example,
`console login -name Ana -tools people` can invite people and edit groups, but gets
`403` when it changes applications or sites. An unknown name is rejected. In **Admin**,
the console shows what the session includes.

- The link works once for five minutes. Its fragment is removed from the URL when you
  open it, and it is exchanged for a fifteen-minute credential.
- The credential lives in the tab's memory. Reloading the page does not end the
  session: the browser keeps it in an `HttpOnly`, `SameSite=Strict` cookie that only
  `POST /_/api/operators/resume` receives. Signing out, revoking, expiry or restarting
  the process ends it.
- `console list` shows IDs, names and expiry times. `console revoke -id ID` revokes a
  pending link or its active session.
- Changes carry the actor `operator:<name>`. Issue, access, exit and revocation are
  recorded. When administrative audit is mandatory, its intents are persisted before
  issuing or revoking.
- A browser session does not recover accounts, configure a GitHub App key, or enrol,
  rotate or revoke connectors. Those actions use the socket.
- The global token still works for compatibility. **Lock** removes its copy from this
  tab, but does not revoke that token in its source file.

For a VPS, open a private transport first:

```sh
ssh -N -L 19090:127.0.0.1:9090 operator@your-edge
# In the terminal on the edge, with authority over its socket:
ozal console login -config /etc/ozal/config.json \
  -name Octavio -url http://127.0.0.1:19090
```

Open the link in the browser on the machine that has the forward. Do not publish the
administrative listener on the Internet, and do not make it the upstream of a public
route. The bootstrap does not make the first visitor the owner.

## People, groups and sessions

In **People** you assign groups when you invite someone, and edit native membership
with a review of the consequences. The update uses a current read; a concurrent change
returns a conflict. When groups change, that person's native sessions are deleted in
the same transaction, and every request checks the current groups, so a sign-in racing
with the removal still fails. Re-inviting someone does not change their memberships.
`GET /_/api/groups` lists the local memberships and route requirements. OIDC groups are
managed by their provider.

At `/_/auth/me`, a visitor sees the applications they may open and their own sessions,
with last-use and expiry dates and the sign-in method. They can sign out one session or
all of them, but cannot see or revoke another person's. The device and passkey list is at
`/_/auth/devices`, and it asks for a recent proof of identity.

## Upgrade an earlier installation

Migration **012** adds connector expiry metadata and a session index. It also ends earlier
OIDC sessions, because their raw `sub` values cannot be converted safely into
issuer-qualified subjects. New OIDC subjects include the issuer. Accounts are never
linked by email. Native accounts and passkeys are kept. Native sessions that recorded the
`passkey` method remain valid; an older session without a recorded method needs a new
sign-in.

Follow the [upgrade and backup procedure](administration.md#upgrade-and-roll-back) and
apply migrations explicitly. Do not run a new version against an unmigrated database.
Reverting the binary after a migration also requires the earlier copy of the database.
