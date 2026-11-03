// Reproducible console acceptance against an isolated real edge. Browser
// authentication is host-issued; credentials never enter stdout or artifacts.
import { execFile } from "node:child_process";
import { promisify } from "node:util";
import { mkdtemp, rm, mkdir, writeFile } from "node:fs/promises";
import { tmpdir } from "node:os";
import { join } from "node:path";
import assert from "node:assert/strict";
import { startDemoEdge } from "./edge.mjs";
const exec = promisify(execFile);
const binary = process.env.OZAL_BIN;
if (!binary) throw new Error("Set OZAL_BIN to the built binary");
const root = await mkdtemp(join(tmpdir(), "sb-product-"));
const captures = process.env.OZAL_CONSOLE_ARTIFACTS;
if (captures) await mkdir(captures, { recursive: true });
const session = `ozal-product-${process.pid}`;
let edge;
let step = "start";
const ab = async (...args) => {
  try { return (await exec("agent-browser", ["--session", session, ...(process.env.OZAL_BROWSER_ARGS ? ["--args", process.env.OZAL_BROWSER_ARGS] : []), "--json", ...args], { maxBuffer: 4 << 20 })).stdout; }
  catch (error) { let detail = String(error.stderr ?? ""); try { detail += JSON.stringify(JSON.parse(error.stdout).error); } catch {} const safe = detail.replace(/operator=[^\s"<>]+/g, "operator=[redacted]").slice(0, 2000); throw new Error(`Browser ${args[0]} ${args[0] === "click" ? args[1] : ""} failed during ${step}: ${safe}`); }
};
const evaluate = async (source) => JSON.parse(await ab("eval", source)).data?.result;
const tabTo = async (href) => {
  await ab("eval", 'document.querySelector(\'a[href="#main-content"]\').focus()');
  for (let i = 0; i < 30; i++) {
    await ab("press", "Tab");
    if (await evaluate("document.activeElement?.getAttribute('href')") === href) return;
  }
  throw new Error(`Keyboard tab order did not reach ${href}`);
};
const failNextRequest = async (path) => evaluate(`(() => { const original = window.fetch.bind(window); let failed = false; window.fetch = (input, init) => { const url = new URL(typeof input === "string" ? input : input.url, location.href); if (!failed && url.pathname === ${JSON.stringify(path)}) { failed = true; window.__acceptanceFailedPath = url.pathname; window.fetch = original; return Promise.resolve(new Response("", { status: 503 })); } return original(input, init); }; return true; })()`);
const failRequestsUntilRecovery = async (path) => evaluate(`(() => { const original = window.fetch.bind(window); window.__acceptanceFailedPath = ""; window.__acceptanceRecover = () => { window.fetch = original; }; window.fetch = (input, init) => { const url = new URL(typeof input === "string" ? input : input.url, location.href); if (url.pathname === ${JSON.stringify(path)}) { window.__acceptanceFailedPath = url.pathname; return Promise.resolve(new Response("", { status: 503 })); } return original(input, init); }; return true; })()`);
const goPage = async (href, title) => {
  await ab("set", "viewport", "1440", "1000");
  await ab("eval", "window.scrollTo(0, 0)");
  await ab("eval", `document.querySelector('nav a[href=\"${href}\"]').focus()`);
  await ab("press", "Enter");
  await ab("wait", "--fn", `location.pathname === ${JSON.stringify(href)} && document.querySelector('#main-content h1')?.textContent?.trim() === ${JSON.stringify(title)}`);
  await ab("eval", "window.scrollTo(0, 0)");
};
const auditPage = async (widths = [[1440, 1000], [1024, 900], [390, 844], [320, 800]]) => {
  for (const [width, height] of widths) {
    await ab("set", "viewport", String(width), String(height));
    const overflow = await evaluate("({ viewport: innerWidth, main: document.querySelector('#main-content')?.getBoundingClientRect().right, offenders: [...document.querySelectorAll('body *')].filter((el) => { const r = el.getBoundingClientRect(); const scroller = el.closest('.overflow-x-auto, nav[aria-label$=\" views\"]'); return !scroller && (r.right > innerWidth + 1 || r.left < -1); }).slice(0, 12).map((el) => ({ tag: el.tagName, className: String(el.className).slice(0, 120), text: (el.textContent || '').trim().slice(0, 80), left: Math.round(el.getBoundingClientRect().left), right: Math.round(el.getBoundingClientRect().right) })) })");
    assert(overflow.main <= width && overflow.offenders.length === 0, `overflow at ${width}: ${JSON.stringify(overflow)}`);
    const result = JSON.parse(await ab("a11y"));
    const violations = result.data?.violations ?? result.data?.results?.violations;
    assert(Array.isArray(violations), "axe returned no violation list");
    assert.equal(violations.length, 0, `axe violations at ${width}: ${JSON.stringify(violations.map((v) => ({ id: v.id, impact: v.impact, help: v.help, nodes: v.nodes?.map((n) => ({ target: n.target, summary: n.failureSummary })) })))}`);
  }
};
const api = async (method, path, body) => {
  const res = await fetch(`${edge.consoleURL}${path}`, { method, headers: { Authorization: `Bearer ${edge.token}`, "Content-Type": "application/json" }, body: body ? JSON.stringify(body) : undefined });
  assert(res.ok, `fixture API ${path}: HTTP ${res.status}`);
  return res.json();
};
try {
  edge = await startDemoEdge({ binary, dir: join(root, "edge"), adminSocket: true, dynamicPorts: true });
  await api("POST", "/_/api/people/invite", { email: "new-operator-test@example.test", groups: ["ops"] });
  // A second group to move the first person into: the editor picks existing groups only.
  await api("POST", "/_/api/people/invite", { email: "staff-member-test@example.test", groups: ["staff"] });
  step = "host operator login";
  const { stdout } = await exec(binary, ["console", "login", "-config", join(edge.dir, "config.json"), "-name", "Acceptance operator", "-url", edge.consoleURL]);
  const url = stdout.split("\n").find((line) => line.startsWith("http"));
  assert(url, "host login returned no URL");
  await ab("open", url);
  await ab("wait", "--text", "Acceptance operator");
  assert.equal(await evaluate("location.hash"), "", "one-use credential remained in the URL");
  await ab("eval", "window.__cspViolations = []; addEventListener('securitypolicyviolation', (event) => window.__cspViolations.push({ directive: event.effectiveDirective, blocked: event.blockedURI })); true");
  step = "keyboard navigation and add existing application";
  await tabTo("/applications");
  await ab("press", "Enter");
  step = "open Applications";
  await ab("wait", "--text", "Applications");
  assert.equal(await evaluate("document.activeElement?.textContent?.trim()"), "Applications", "keyboard navigation did not focus the page heading");
  await ab("find", "role", "button", "click", "--name", "Refresh configuration", "--exact");
  step = "refresh Applications configuration";
  await ab("wait", "--fn", "document.querySelector('#main-content')?.textContent?.includes('private ·')");
  step = "add existing backend application";
  await ab("find", "role", "button", "click", "--name", "Add application", "--exact");
  await ab("fill", "#app-target", `http://127.0.0.1:${edge.upstreamPorts.grafana}`);
  step = "application wizard address and access steps";
  await ab("find", "role", "button", "click", "--name", "Next", "--exact");
  await ab("fill", "#app-host", `extra.demo.localhost:${edge.port}`);
  await ab("find", "role", "button", "click", "--name", "Next", "--exact");
  await ab("fill", "#app-groups", "ops");
  await ab("fill", "#app-invite", "invited-in-flow@example.test");
  await ab("find", "role", "button", "click", "--name", "Review", "--exact");
  step = "review application and acknowledge consequence";
  await ab("wait", "--text", "I checked what serves it");
  await ab("find", "role", "checkbox", "check", "--name", "I checked what serves it and who can open it. I will try it before moving a live address.", "--exact");
  await ab("wait", "--fn", "document.querySelector('dialog[open] input[type=\"checkbox\"], [role=\"dialog\"] input[type=\"checkbox\"]')?.checked === true");
  await ab("find", "role", "button", "click", "--name", "Save application", "--exact");
  step = "confirm application save";
  await ab("wait", "--text", "Save this application?");
  await ab("find", "role", "button", "click", "--name", "Confirm and save", "--exact");
  step = "wait for saved application state";
  await ab("wait", "--text", "Saved. Point its DNS here");
  step = "connection check after saving";
  // The address resolves to this machine: the one-time probe must come back
  // through the public listener, and the invitation link is offered to copy.
  await ab("wait", "--text", "reached this edge");
  await ab("wait", "--text", "invited-in-flow@example.test");
  assert.equal(await evaluate("[...document.querySelectorAll('dialog[open] button, [role=\"dialog\"] button')].some((b) => b.textContent.includes('Copy link'))"), true, "the invitation link was not offered");
  await auditPage([[390, 844], [1440, 1000]]);
  await ab("find", "role", "button", "click", "--name", "Done", "--exact");
  const setup = await api("GET", "/_/api/setup");
  const added = setup.routes.find((r) => r.host === `extra.demo.localhost:${edge.port}`);
  assert.deepEqual(added?.access?.required_groups, ["ops"]);
  const invited = (await api("GET", "/_/api/people")).users.find((p) => p.email === "invited-in-flow@example.test");
  assert.deepEqual(invited?.groups, ["ops"], "the invitation from the flow did not carry the application's group");
  step = "static artifact upload and rollback";
  const before = (await api("GET", "/_/api/sites")).sites.find((s) => s.name === "blog").live;
  const pack = join(root, "artifact"); await mkdir(pack);
  await writeFile(join(pack, "index.html"), "<!doctype html><title>Product acceptance</title><h1>Published from Applications</h1>");
  const archive = join(root, "artifact.tar.gz"); await exec("tar", ["-czf", archive, "-C", pack, "."]);
  await ab("find", "role", "button", "click", "--name", `More actions for blog.demo.localhost:${edge.port}`, "--exact");
  await ab("find", "role", "menuitem", "click", "--name", "Publish a new version");
  await ab("upload", 'dialog[open] input[type="file"], [role="dialog"] input[type="file"]', archive);
  await ab("find", "role", "button", "click", "--name", "Review publication", "--exact");
  await ab("find", "role", "button", "click", "--name", "Publish artifact", "--exact");
  await ab("wait", "--text", "is live. Check its content");
  await ab("press", "Escape");
  let content = (await exec("curl", ["-fsS", "--max-time", "10", "-H", `Host: blog.demo.localhost:${edge.port}`, `http://127.0.0.1:${edge.port}/`])).stdout;
  assert(content.includes("Published from Applications"));
  await api("POST", "/_/api/sites/blog/rollback", { id: before });
  content = (await exec("curl", ["-fsS", "--max-time", "10", "-H", `Host: blog.demo.localhost:${edge.port}`, `http://127.0.0.1:${edge.port}/`])).stdout;
  assert(!content.includes("Published from Applications"), "rollback did not restore previous bytes");
  step = "new static website from uploaded files";
  await ab("find", "role", "button", "click", "--name", "Add application", "--exact");
  await ab("find", "role", "radio", "click", "--name", "A static website");
  await ab("upload", 'dialog[open] input[type="file"], [role="dialog"] input[type="file"]', archive);
  await ab("find", "role", "button", "click", "--name", "Next", "--exact");
  await ab("fill", "#app-host", `fresh.demo.localhost:${edge.port}`);
  await ab("find", "role", "button", "click", "--name", "Next", "--exact");
  await ab("find", "role", "radio", "click", "--name", "Everyone");
  await ab("find", "role", "button", "click", "--name", "Review", "--exact");
  await ab("wait", "--text", "uploads artifact.tar.gz");
  await ab("find", "role", "checkbox", "check", "--name", "I checked what serves it and who can open it. I will try it before moving a live address.", "--exact");
  await ab("wait", "--fn", "document.querySelector('dialog[open] input[type=\"checkbox\"], [role=\"dialog\"] input[type=\"checkbox\"]')?.checked === true");
  await ab("find", "role", "button", "click", "--name", "Save application", "--exact");
  await ab("find", "role", "button", "click", "--name", "Confirm and save", "--exact");
  await ab("wait", "--text", "Saved. Point its DNS here");
  await ab("find", "role", "button", "click", "--name", "Done", "--exact");
  content = (await exec("curl", ["-fsS", "--max-time", "10", "-H", `Host: fresh.demo.localhost:${edge.port}`, `http://127.0.0.1:${edge.port}/`])).stdout;
  assert(content.includes("Published from Applications"), "the new static website does not serve the uploaded build");
  assert.equal((await api("GET", "/_/api/setup")).routes.find((r) => r.host === `fresh.demo.localhost:${edge.port}`)?.site, "fresh-demo-localhost", "the site name was not derived from the address");
  step = "membership consequence and cancellation";
  await ab("click", 'a[href="/people"]');
  await ab("wait", "--text", "new-operator-test@example.test");
  await ab("find", "role", "button", "click", "--name", "Edit groups for new-operator-test@example.test", "--exact");
  await ab("find", "role", "checkbox", "click", "--name", "ops", "--exact");
  await ab("find", "role", "checkbox", "click", "--name", "staff", "--exact");
  await ab("find", "role", "button", "click", "--name", "Review change", "--exact");
  await ab("press", "Escape");
  let people = await api("GET", "/_/api/people");
  assert.deepEqual(people.users.find((p) => p.email === "new-operator-test@example.test").groups, ["ops"], "cancel changed membership");
  await ab("find", "role", "button", "click", "--name", "Review change", "--exact");
  assert.equal(await evaluate("document.activeElement?.textContent?.trim()"), "Back", "consequence dialog did not focus the safe return action initially");
  await ab("press", "Tab");
  assert.equal(await evaluate("document.activeElement?.textContent?.trim()"), "Save membership", "dialog Tab order did not reach confirmation");
  await ab("press", "Tab");
  assert.equal(await evaluate("document.activeElement?.getAttribute('aria-label')"), "Close", "dialog focus did not wrap to its close control");
  await ab("press", "Escape");
  assert.equal(await evaluate("document.querySelectorAll('dialog[open], [role=\"dialog\"]').length"), 1, "Escape did not close only the membership review");
  await ab("wait", "--fn", "document.activeElement?.textContent?.trim() === 'Review change'");
  people = await api("GET", "/_/api/people");
  assert.deepEqual(people.users.find((p) => p.email === "new-operator-test@example.test").groups, ["ops"], "Escape changed membership");
  await ab("find", "role", "button", "click", "--name", "Review change", "--exact");
  await ab("find", "role", "button", "click", "--name", "Save membership", "--exact");
  await ab("wait", "--text", "Membership saved");
  people = await api("GET", "/_/api/people");
  assert.deepEqual(people.users.find((p) => p.email === "new-operator-test@example.test").groups, ["staff"]);
  step = "an empty group is created first, offered to invitations, and deleted";
  await goPage("/groups", "People");
  await ab("find", "role", "button", "click", "--name", "New group", "--exact");
  await ab("fill", "#group-name", "contractors");
  await ab("find", "role", "button", "click", "--name", "Create group", "--exact");
  await ab("wait", "--text", "contractors created");
  let catalog = (await api("GET", "/_/api/groups")).groups;
  assert.equal(catalog.find((g) => g.name === "contractors")?.defined, true, "the empty group was not created");
  // From the group itself, Invite person opens the invitation with it ticked.
  await ab("find", "role", "link", "click", "--name", "Invite a person into contractors", "--exact");
  await ab("wait", "--fn", "[...document.querySelectorAll('dialog[open] label')].some((l) => l.textContent.trim() === 'contractors' && l.querySelector('input').checked)");
  assert.equal(await evaluate("location.search"), "", "the invite parameter stayed in the address");
  await ab("press", "Escape");
  await goPage("/groups", "People");
  await ab("find", "role", "button", "click", "--name", "Delete contractors", "--exact");
  await ab("wait", "--text", "contractors deleted");
  catalog = (await api("GET", "/_/api/groups")).groups;
  assert.ok(!catalog.some((g) => g.name === "contractors"), "the unused group was not deleted");
  step = "Overview sites error and retry";
  await goPage("/sites", "Sites");
  await failNextRequest("/_/api/sites");
  await goPage("/overview", "Overview");

  await ab("wait", "--text", "Sites could not be read");
  await ab("find", "text", "Retry", "click");
  await ab("wait", "--text", "What is live, and where it came from");
  await auditPage([[1440, 1000], [390, 844]]);

  step = "CI publishing error and retry";
  await goPage("/applications", "Applications");
  await goPage("/sites", "Sites");
  await ab("wait", "--text", "Publishing from GitHub");
  await ab("wait", "--text", "No website publishes from GitHub yet");
  await failRequestsUntilRecovery("/_/api/projects");
  const outageShown = await evaluate(`(async () => { document.querySelector('button[aria-label="Refresh publishing"]').click(); const until = Date.now() + 5000; while (Date.now() < until) { if (document.body.innerText.includes("HTTP 503")) return true; await new Promise((resolve) => setTimeout(resolve, 25)); } return false; })()`);
  assert.equal(outageShown, true, "the publishing error was not visible while its endpoint returned 503");
  assert.equal(await evaluate("window.__acceptanceFailedPath === '/_/api/projects'"), true, "the projects endpoint did not receive the injected outage");
  await evaluate("window.__acceptanceRecover()");
  await ab("find", "role", "button", "click", "--name", "Refresh publishing", "--exact");
  await ab("wait", "--text", "Optional: connect GitHub");
  await ab("wait", "--text", "No website publishes from GitHub yet");
  await auditPage([[1440, 1000], [390, 844]]);

  step = "Connector registry error, retry and empty state";
  await failNextRequest("/_/api/connectors");
  await goPage("/connectors", "Connectors");
  await ab("wait", "--text", "Connector registry could not be read");
  await ab("find", "role", "button", "click", "--name", "Retry registry read", "--exact");
  await ab("wait", "--text", "No private hosts connected");
  await auditPage([[1440, 1000], [390, 844]]);

  step = "Sites hostname mapping error and recovery";
  await goPage("/sites", "Sites");
  await failNextRequest("/_/api/status");
  await ab("find", "role", "button", "click", "--name", "Refresh sites", "--exact");
  await ab("wait", "--text", "Hostname mappings could not be refreshed");
  assert.equal(await evaluate("document.querySelector('dialog[open], [role=\"dialog\"]') === null"), true);
  await ab("find", "role", "button", "click", "--name", "Refresh sites", "--exact");
  await ab("wait", "--fn", "!document.body.innerText.includes('Hostname mappings could not be refreshed')");
  await auditPage([[1440, 1000], [390, 844]]);

  step = "Sites rollback review cancellation and confirmation";
  const liveBeforeUI = (await api("GET", "/_/api/sites")).sites.find((s) => s.name === "blog").live;
  const olderBlog = (await api("GET", "/_/api/sites")).sites.find((s) => s.name === "blog").deployments.find((d) => d.id !== liveBeforeUI);
  assert(olderBlog, "rollback fixture has no retained deployment");
  await ab("find", "role", "button", "click", "--name", `Restore ${olderBlog.id.slice(0, 12)} for blog`, "--exact");
  await ab("wait", "--text", "Restore this version?");
  await ab("press", "Escape");
  assert.equal((await api("GET", "/_/api/sites")).sites.find((s) => s.name === "blog").live, liveBeforeUI, "cancel changed live deployment");
  await ab("find", "role", "button", "click", "--name", `Restore ${olderBlog.id.slice(0, 12)} for blog`, "--exact");
  await ab("find", "role", "button", "click", "--name", "Restore version", "--exact");
  await ab("wait", "--text", "blog now serves");
  assert.equal((await api("GET", "/_/api/sites")).sites.find((s) => s.name === "blog").live, olderBlog.id, "confirmed Sites rollback did not switch the live build");

  step = "Application removal review cancellation and confirmation";
  const appHost = `extra.demo.localhost:${edge.port}`;
  await goPage("/applications", "Applications");
  await ab("find", "role", "button", "click", "--name", "Refresh configuration", "--exact");
  await ab("wait", "--text", appHost);
  await ab("find", "role", "button", "click", "--name", `More actions for ${appHost}`, "--exact");
  await ab("find", "role", "menuitem", "click", "--name", "Remove");
  await ab("wait", "--text", "Remove this application hostname?");
  await ab("eval", "window.__dialogCancelEvents = 0; document.querySelector('dialog[open]').addEventListener('cancel', () => window.__dialogCancelEvents++)");
  await ab("press", "Escape");
  const escapeState = await evaluate("({ open: !!document.querySelector('dialog[open]'), cancelEvents: window.__dialogCancelEvents, active: document.activeElement?.outerHTML?.slice(0, 160) })");
  assert.equal(escapeState.open, false, `Escape did not close the confirmation dialog: ${JSON.stringify(escapeState)}`);
  await ab("wait", "--fn", "document.querySelector('dialog[open]') === null");
  assert((await api("GET", "/_/api/setup")).routes.some((r) => r.host === appHost), "cancelling removal removed the route");
  await ab("find", "role", "button", "click", "--name", `More actions for ${appHost}`, "--exact");
  await ab("find", "role", "menuitem", "click", "--name", "Remove");
  await ab("find", "role", "button", "click", "--name", "Remove application", "--exact");
  await ab("wait", "--fn", `![...document.querySelectorAll('#main-content h2')].some((el) => el.textContent.trim() === ${JSON.stringify(appHost)})`);
  assert.equal((await api("GET", "/_/api/setup")).routes.some((r) => r.host === appHost), false, "confirmed removal left the route configured");

  step = "require a GitHub connection before creating a GitHub website";
  await ab("find", "role", "button", "click", "--name", "Add application", "--exact");
  await ab("find", "role", "radio", "click", "--name", "A website built from GitHub");
  await ab("find", "role", "button", "click", "--name", "Next", "--exact");
  await ab("wait", "--text", "Connect GitHub first");
  assert.equal((await api("GET", "/_/api/setup")).routes.some((r) => r.host === `gitflow.demo.localhost:${edge.port}`), false, "GitHub website was created without a connected repository");
  await ab("find", "role", "button", "click", "--name", "Close", "--exact");

  step = "responsive layouts and accessibility";
  const pages = [["/overview", "Overview"], ["/applications", "Applications"], ["/routes", "Hostnames"], ["/sites", "Sites"], ["/domains", "Domains"], ["/people", "People"], ["/groups", "People"], ["/administrators", "Administrators"], ["/connectors", "Connectors"], ["/activity", "Activity"]];
  for (const [href, title] of pages) {
    await goPage(href, title);
    await auditPage([[1440, 1000], [1024, 900], [390, 844], [320, 800]]);
    if (captures) {
      for (const [width, height] of [[1440, 1000], [390, 844]]) {
        await ab("set", "viewport", String(width), String(height));
        await ab("eval", "window.scrollTo(0, 0)");
        await ab("screenshot", join(captures, `${href.slice(1)}-${width}.png`), "--full");
      }
    }
    if (href === "/applications") {
      await ab("set", "viewport", "1440", "1000");
      await ab("screenshot", join(root, "applications-1440.png"));
      await ab("set", "viewport", "390", "844");
      await ab("screenshot", join(root, "applications-390.png"));
    }
  }
  step = "mobile Ozal navigation and keyboard focus";
  await ab("set", "viewport", "390", "844");
  await ab("find", "role", "button", "click", "--name", "Open menu", "--exact");
  await ab("wait", "--fn", "document.querySelector('#mobile-menu')?.open === true");
  await ab("wait", "--fn", "document.activeElement?.closest('#mobile-menu') !== null");
  await ab("eval", "document.querySelector('#mobile-menu button[aria-label=\"Close menu\"]').focus()");
  await ab("press", "Shift+Tab");
  assert.equal(await evaluate("document.activeElement?.getAttribute('href')"), "/overview", "backward Tab did not reach the drawer brand link");
  await ab("press", "Shift+Tab");
  assert.equal(await evaluate("document.activeElement?.getAttribute('aria-label')"), "Acceptance operator", "backward Tab escaped the mobile navigation");
  await ab("press", "Tab");
  assert.equal(await evaluate("document.activeElement?.getAttribute('href')"), "/overview", "Tab did not wrap to the first control in mobile navigation");
  if (captures) await ab("screenshot", join(captures, "navigation-390.png"), "--full");
  await auditPage([[390, 844], [320, 800]]);
  await ab("press", "Escape");
  await ab("wait", "--fn", "document.querySelector('#mobile-menu')?.open === false && document.activeElement?.getAttribute('aria-label') === 'Open menu'");
  await ab("find", "role", "button", "click", "--name", "Open menu", "--exact");
  await ab("click", "#mobile-menu a[href=\"/sites\"]");
  await ab("wait", "--fn", "location.pathname === '/sites' && !document.querySelector('#mobile-menu')?.open && document.activeElement?.textContent?.trim() === 'Sites'");
  step = "signing out hides the console entirely";
  await ab("set", "viewport", "1440", "1000");
  await goPage("/people", "People");
  await ab("wait", "--text", "new-operator-test@example.test");
  await ab("find", "role", "button", "click", "--name", "Acceptance operator", "--exact");
  await ab("find", "role", "button", "click", "--name", "Sign out", "--exact");
  await ab("wait", "--text", "Use a host login");
  if (captures) {
    for (const [width, height] of [[1440, 1000], [768, 1024], [390, 844], [320, 800]]) {
      await ab("set", "viewport", String(width), String(height));
      await ab("screenshot", join(captures, `sign-in-${width}.png`), "--full");
    }
  }
  assert.equal(await evaluate("document.body.innerText.includes('new-operator-test@example.test')"), false, "signing out kept the people list visible");
  for (const name of ["Applications", "Connectors", "Activity", "Routing & health", "Administrators"]) {
    assert.equal(await evaluate(`document.body.innerText.includes(${JSON.stringify(name)})`), false, `the signed-out screen still shows ${name}`);
  }
  assert.equal(await evaluate("document.querySelectorAll('nav').length"), 0, "the signed-out screen still has navigation");
  assert.equal(await evaluate("(async () => (await fetch('/_/api/people')).status)()"), 401, "anonymous people listing remained available");
  assert.equal(await evaluate("(async () => (await fetch('/_/api/sites')).status)()"), 401, "anonymous site listing remained available");
  assert.equal(await evaluate("(async () => (await fetch('/_/events')).status)()"), 401, "anonymous event stream remained available");
  for (const path of ["/_/status", "/_/api/status", "/_/overview"]) assert.equal(await evaluate(`(async () => (await fetch(${JSON.stringify(path)})).status)()`), 401, `anonymous ${path} remained available`);
  assert.equal(await evaluate("(async () => (await fetch('/_/healthz')).status)()"), 200, "readiness probe unavailable");
  const cspViolations = await evaluate("window.__cspViolations");
  assert.deepEqual(cspViolations, [], `CSP violations occurred while using menus and dialogs: ${JSON.stringify(cspViolations)}`);
  console.log("Product console: host login, URL cleanup, app add/remove review, artifact upload/rollback, membership keyboard/cancel/save, Projects and Connector error recovery, Sites rollback, all console pages at four widths with axe OK");
  console.log(`Screenshots: ${root}`);
} catch (error) {
  console.error(`Product acceptance failed at ${step}: ${error.message}`);
  if (step !== "host operator login") { console.error(await ab("snapshot", "-i").catch(() => "snapshot unavailable")); }
  process.exitCode = 1;
} finally {
  await exec("agent-browser", ["--session", session, "close"]).catch(() => {});
  if (edge) await edge.stop();
  // Retain only screenshots, never config/state or fixture credentials.
  await rm(join(root, "edge"), { recursive: true, force: true });
  await rm(join(root, "artifact"), { recursive: true, force: true });
  await rm(join(root, "artifact.tar.gz"), { force: true });
}
