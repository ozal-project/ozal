// A real Ozal edge on loopback with demo data, for reviewing the console
// and recording the tour (SITE-004). Nothing here is mocked: the binary is the
// one built from this tree, the upstreams are small HTTP servers, and the
// events, deployments and invitations are made through the real admin API.
//
//   node scripts/demo/edge.mjs [--binary PATH] [--dir DIR]   # runs until Ctrl-C
//
// or import { startDemoEdge } from a script (scripts/demo/tour.mjs does).
import { spawn, execFileSync } from "node:child_process";
import { createServer } from "node:http";
import { mkdirSync, rmSync, writeFileSync, existsSync } from "node:fs";
import { join, resolve } from "node:path";
import { homedir } from "node:os";
import { fileURLToPath } from "node:url";

export const PORT = 18888;
export const ADMIN_PORT = 19090;
// A parent domain of its own: Chrome treats bare "localhost" as a public suffix and
// refuses both passkeys scoped to it and cookies shared under it.
export const DOMAIN = "demo.localhost";
export const LOGIN = `http://auth.${DOMAIN}:${PORT}`;
const APPS = [
  { name: "grafana", title: "Grafana", port: 19101, blurb: "Dashboards for the platform team", access: { allowed_domains: ["acme.dev"] } },
  { name: "billing", title: "Billing", port: 19102, blurb: "Invoices and payouts", access: { allowed_emails: ["ana@acme.dev"], context_rule: { when: { session_device_bound: false }, then: "deny" } } },
  { name: "wiki", title: "Wiki", port: 19103, blurb: "How we work", access: { allowed_domains: ["acme.dev"], max_auth_age_minutes: 15 } },
];

const page = (title, blurb) => `<!doctype html><meta charset="utf-8"><title>${title}</title>
<body style="margin:0;min-height:100vh;display:grid;place-items:center;background:#101014;color:#ededef;font:16px system-ui,sans-serif">
<main style="text-align:center"><p style="color:#8b8b95;font-size:13px;letter-spacing:.08em;text-transform:uppercase">demo app</p>
<h1 style="margin:.2em 0;font-size:2.6rem">${title}</h1><p style="color:#b4b4bb">${blurb}</p></main>`;

function upstream(app) {
  return new Promise((ok) => {
    const server = createServer((req, res) => {
      res.writeHead(200, { "content-type": "text/html; charset=utf-8" });
      res.end(page(app.title, app.blurb));
    }).listen(app.port, "127.0.0.1", () => ok(server));
  });
}

async function admin(token, method, path, body, headers = {}) {
  const res = await fetch(`http://127.0.0.1:${ADMIN_PORT}${path}`, {
    method,
    headers: { Authorization: `Bearer ${token}`, ...(body && !(body instanceof Uint8Array) ? { "Content-Type": "application/json" } : {}), ...headers },
    body: body instanceof Uint8Array ? body : body ? JSON.stringify(body) : undefined,
  });
  const text = await res.text();
  if (!res.ok) throw new Error(`${method} ${path}: ${res.status} ${text}`);
  return text ? JSON.parse(text) : {};
}

function archive(dir, files) {
  rmSync(dir, { recursive: true, force: true });
  mkdirSync(dir, { recursive: true });
  for (const [name, content] of Object.entries(files)) writeFileSync(join(dir, name), content);
  return new Uint8Array(execFileSync("tar", ["-czf", "-", "-C", dir, "."], { maxBuffer: 1 << 24 }));
}

// extraRoutes are appended to the demo routes, for scripts that need a host
// the demo does not have (an app on another domain, for instance).
export async function startDemoEdge({ binary, dir = join(homedir(), ".cache", "ozal-demo"), adminSocket = false, extraRoutes = [] } = {}) {
  if (!binary || !existsSync(binary)) throw new Error("pass the ozal binary (--binary)");
  rmSync(dir, { recursive: true, force: true });
  mkdirSync(join(dir, "sites"), { recursive: true });
  const token = "demo-" + Math.random().toString(36).slice(2, 12);
  const servers = await Promise.all(APPS.map(upstream));

  const config = {
    listen: `127.0.0.1:${PORT}`,
    admin: { listen: `127.0.0.1:${ADMIN_PORT}`, token: "env:SB_DEMO_TOKEN", ...(adminSocket ? { socket: join(dir, "admin.sock") } : {}) },
    auth: { cookie_domain: DOMAIN, passkeys: { rp_id: DOMAIN, login_url: LOGIN, display_name: "Acme" } },
    pages: { dir: join(dir, "sites"), keep: 10 },
    routes: [
      ...APPS.map((a) => ({ host: `${a.name}.${DOMAIN}:${PORT}`, target: `http://127.0.0.1:${a.port}`, health_path: "/", access: a.access })),
      { host: `blog.${DOMAIN}:${PORT}`, site: "blog" },
      { host: `docs.${DOMAIN}:${PORT}`, site: "docs" },
      // Nothing listens here: the console should say so.
      { host: `reports.${DOMAIN}:${PORT}`, target: "http://127.0.0.1:19199", health_path: "/" },
      ...extraRoutes,
    ],
  };
  writeFileSync(join(dir, "config.json"), JSON.stringify(config, null, 2));
  const env = { ...process.env, SB_DEMO_TOKEN: token };
  // A fresh database must be migrated before the edge will start.
  execFileSync(binary, ["migrate", "-db", join(dir, "state.db")], { env, stdio: "pipe" });
  const child = spawn(binary, ["-config", join(dir, "config.json"), "-db", join(dir, "state.db")], { env, stdio: ["ignore", "pipe", "pipe"] });
  let log = "";
  child.stdout.on("data", (d) => (log += d));
  child.stderr.on("data", (d) => (log += d));
  for (let i = 0; ; i++) {
    try { if ((await fetch(`http://127.0.0.1:${ADMIN_PORT}/_/healthz`)).ok) break; } catch { /* not up yet */ }
    if (i > 100 || child.exitCode !== null) throw new Error("the edge did not start:\n" + log.slice(-800));
    await new Promise((r) => setTimeout(r, 100));
  }

  const edge = {
    token,
    dir,
    consoleURL: `http://127.0.0.1:${ADMIN_PORT}`,
    appURL: (name) => `http://${name}.${DOMAIN}:${PORT}/`,
    deploy: (site, files) => admin(token, "POST", `/_/api/sites/${site}/deployments`, archive(join(dir, "pack", site), files), { "Content-Type": "application/gzip" }),
    invite: (email, name) => admin(token, "POST", "/_/api/people/invite", { email, name, groups: [], return_to: "", ttl_minutes: 60 }),
    // A visitor with no session knocks on a protected app: a real refusal event.
    knock: (name) => fetch(`http://127.0.0.1:${PORT}/`, { headers: { Host: `${name}.${DOMAIN}:${PORT}` }, redirect: "manual" }),
    async stop() {
      child.kill("SIGTERM");
      await Promise.all(servers.map((s) => new Promise((r) => s.close(r))));
    },
  };

  // The first state of an edge that has been running for a while.
  const site = (title, note) => ({ "index.html": `<!doctype html><meta charset="utf-8"><title>${title}</title><body style="font:18px system-ui;background:#101014;color:#ededef;padding:3rem"><h1>${title}</h1><p>${note}</p>` });
  for (const n of ["First post", "Second post", "Release notes"]) await edge.deploy("blog", site("Acme blog", n));
  for (const n of ["Getting started", "Self-hosting guide"]) await edge.deploy("docs", site("Acme docs", n));
  // Invitations that nobody has used yet; the tour opens Bob's.
  edge.invites = {};
  for (const [key, email, name] of [["bob", "bob@other.org", "Bob"], ["carla", "carla@acme.dev", "Carla"]]) edge.invites[key] = (await edge.invite(email, name)).link;
  for (const name of ["grafana", "grafana", "billing", "wiki", "grafana"]) await edge.knock(name);
  return edge;
}

if (process.argv[1] === fileURLToPath(import.meta.url)) {
  const arg = (flag, fallback) => (process.argv.includes(flag) ? process.argv[process.argv.indexOf(flag) + 1] : fallback);
  const binary = resolve(arg("--binary", "ozal"));
  const edge = await startDemoEdge({ binary, dir: arg("--dir", undefined) });
  console.log(`demo edge ready\n  console  ${edge.consoleURL}/\n  apps     ${["grafana", "billing", "wiki", "blog", "docs"].map(edge.appURL).join("  ")}\n  login    ${LOGIN}/_/auth/login\n  token    ${edge.token}\nCtrl-C stops it.`);
  process.on("SIGINT", async () => { await edge.stop(); process.exit(0); });
}
