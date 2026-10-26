// Reproducible console acceptance against an isolated real edge. Browser
// authentication is host-issued; credentials never enter stdout or artifacts.
import { execFile } from "node:child_process";
import { promisify } from "node:util";
import { mkdtemp, rm, mkdir, writeFile } from "node:fs/promises";
import { tmpdir } from "node:os";
import { join } from "node:path";
import assert from "node:assert/strict";
import { startDemoEdge, PORT } from "./edge.mjs";
const exec = promisify(execFile);
const binary = process.env.SWITCHBOARD_BIN;
if (!binary) throw new Error("Set SWITCHBOARD_BIN to the built binary");
const root = await mkdtemp(join(tmpdir(), "sb-product-"));
const session = `switchboard-product-${process.pid}`;
let edge;
let step = "start";
const ab = async (...args) => {
  try { return (await exec("agent-browser", ["--session", session, ...(process.env.SWITCHBOARD_BROWSER_ARGS ? ["--args", process.env.SWITCHBOARD_BROWSER_ARGS] : []), "--json", ...args], { maxBuffer: 4 << 20 })).stdout; }
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
const goPage = async (href, title) => {
  await ab("set", "viewport", "1440", "1000");
  await ab("eval", "window.scrollTo(0, 0)");
  await ab("eval", `document.querySelector('nav[aria-label=\"Console\"] a[href=\"${href}\"]').focus()`);
  await ab("press", "Enter");
  await ab("wait", "--fn", `document.querySelector('#main-content h1')?.textContent?.trim() === ${JSON.stringify(title)}`);
  await ab("eval", "window.scrollTo(0, 0)");
};
const auditPage = async (widths = [[1440, 1000], [1024, 900], [390, 844], [320, 800]]) => {
  for (const [width, height] of widths) {
    await ab("set", "viewport", String(width), String(height));
    const overflow = await evaluate("({ viewport: innerWidth, main: document.querySelector('#main-content')?.getBoundingClientRect().right, offenders: [...document.querySelectorAll('body *')].filter((el) => { const r = el.getBoundingClientRect(); const scroller = el.closest('.overflow-x-auto, nav[aria-label=\"Console sections\"]'); return !scroller && (r.right > innerWidth + 1 || r.left < -1); }).slice(0, 12).map((el) => ({ tag: el.tagName, className: String(el.className).slice(0, 120), text: (el.textContent || '').trim().slice(0, 80), left: Math.round(el.getBoundingClientRect().left), right: Math.round(el.getBoundingClientRect().right) })) })");
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
  edge = await startDemoEdge({ binary, dir: join(root, "edge"), adminSocket: true });
  await api("POST", "/_/api/people/invite", { email: "new-operator-test@example.test", groups: ["ops"] });
  step = "host operator login";
  const { stdout } = await exec(binary, ["console", "login", "-config", join(edge.dir, "config.json"), "-name", "Acceptance operator", "-url", edge.consoleURL]);
  const url = stdout.split("\n").find((line) => line.startsWith("http"));
  assert(url, "host login returned no URL");
  await ab("open", url);
  await ab("wait", "--text", "Acceptance operator");
  assert.equal(await evaluate("location.hash"), "", "one-use credential remained in the URL");
  step = "keyboard navigation and add existing application";
  await tabTo("/applications");
  await ab("press", "Enter");
  step = "open Applications";
  await ab("wait", "--text", "Applications");
  assert.equal(await evaluate("document.activeElement?.textContent?.trim()"), "Applications", "keyboard navigation did not focus the page heading");
  await ab("find", "role", "button", "click", "--name", "Refresh configuration", "--exact");
  step = "refresh Applications configuration";
  await ab("wait", "--fn", "document.querySelector('#main-content')?.textContent?.includes('Static publishing')");
  await ab("find", "role", "button", "click", "--name", "Add existing website", "--exact");
  await ab("fill", "#app-host", `extra.demo.localhost:${PORT}`);
  await ab("fill", "#app-target", "http://127.0.0.1:19101");
  await ab("fill", "#app-groups", "ops");
  await ab("find", "role", "button", "click", "--name", "Review configuration", "--exact");
  await ab("wait", "--text", "I reviewed the destination");
  await ab("check", 'input[type="checkbox"]');
  await ab("find", "role", "button", "click", "--name", "Save reviewed application", "--exact");
  await ab("click", "dialog[open] button:last-child");
  await ab("wait", "--text", "Application saved.");
  const setup = await api("GET", "/_/api/setup");
  const added = setup.routes.find((r) => r.host === `extra.demo.localhost:${PORT}`);
  assert.deepEqual(added?.access?.required_groups, ["ops"]);
  step = "static artifact upload and rollback";
  const before = (await api("GET", "/_/api/sites")).sites.find((s) => s.name === "blog").live;
  const pack = join(root, "artifact"); await mkdir(pack);
  await writeFile(join(pack, "index.html"), "<!doctype html><title>Product acceptance</title><h1>Published from Applications</h1>");
  const archive = join(root, "artifact.tar.gz"); await exec("tar", ["-czf", archive, "-C", pack, "."]);
  await ab("find", "text", "Publish an artifact to blog", "click", "--exact");
  await ab("upload", 'details[open] input[type="file"]', archive);
  await ab("find", "role", "button", "click", "--name", "Review publication", "--exact");
  await ab("click", "dialog[open] button:last-child");
  await ab("wait", "--text", "is live. Check its content");
  let content = (await exec("curl", ["-fsS", "--max-time", "10", "-H", `Host: blog.demo.localhost:${PORT}`, `http://127.0.0.1:${PORT}/`])).stdout;
  assert(content.includes("Published from Applications"));
  await api("POST", "/_/api/sites/blog/rollback", { id: before });
  content = (await exec("curl", ["-fsS", "--max-time", "10", "-H", `Host: blog.demo.localhost:${PORT}`, `http://127.0.0.1:${PORT}/`])).stdout;
  assert(!content.includes("Published from Applications"), "rollback did not restore previous bytes");
  step = "membership consequence and cancellation";
  await ab("click", 'a[href="/people"]');
  await ab("wait", "--text", "new-operator-test@example.test");
  await ab("find", "role", "button", "click", "--name", "Edit groups for new-operator-test@example.test", "--exact");
  await ab("fill", 'input[id^="groups-"]', "staff");
  await ab("find", "role", "button", "click", "--name", "Review change", "--exact");
  await ab("press", "Escape");
  let people = await api("GET", "/_/api/people");
  assert.deepEqual(people.users.find((p) => p.email === "new-operator-test@example.test").groups, ["ops"], "cancel changed membership");
  await ab("find", "role", "button", "click", "--name", "Review change", "--exact");
  assert.equal(await evaluate("document.activeElement?.textContent?.trim()"), "Cancel", "consequence dialog did not focus Cancel initially");
  await ab("press", "Tab");
  assert.equal(await evaluate("document.activeElement?.textContent?.trim()"), "Save membership", "dialog Tab order did not reach confirmation");
  await ab("press", "Tab");
  assert.equal(await evaluate("document.activeElement?.textContent?.trim()"), "Cancel", "dialog focus did not remain contained");
  await ab("press", "Escape");
  assert.equal(await evaluate("document.querySelector('dialog[open]') === null"), true, "Escape did not close membership review");
  await ab("wait", "--fn", "document.activeElement?.textContent?.trim() === 'Review change'");
  people = await api("GET", "/_/api/people");
  assert.deepEqual(people.users.find((p) => p.email === "new-operator-test@example.test").groups, ["ops"], "Escape changed membership");
  await ab("find", "role", "button", "click", "--name", "Review change", "--exact");
  await ab("click", "dialog[open] button:last-child");
  await ab("wait", "--text", "Membership saved.");
  people = await api("GET", "/_/api/people");
  assert.deepEqual(people.users.find((p) => p.email === "new-operator-test@example.test").groups, ["staff"]);
  step = "Overview sites error and retry";
  await goPage("/sites", "Sites & deployments");
  await failNextRequest("/_/api/sites");
  await goPage("/overview", "Overview");

  await ab("wait", "--text", "Sites could not be read");
  await ab("find", "text", "Retry", "click");
  await ab("wait", "--text", "What is live, and where it came from");
  await auditPage([[1440, 1000], [390, 844]]);

  step = "CI publishing error and retry";
  await goPage("/applications", "Applications");
  await goPage("/sites", "Sites & deployments");
  await ab("wait", "--text", "Who may publish from CI");
  await failNextRequest("/_/api/projects");
  await ab("find", "role", "button", "click", "--name", "Read projects & GitHub", "--exact");
  await ab("wait", "--fn", "window.__acceptanceFailedPath === '/_/api/projects'");
  await ab("wait", "--text", "HTTP 503");
  await ab("find", "role", "button", "click", "--name", "Read projects & GitHub", "--exact");
  await ab("wait", "--text", "Connect your own GitHub App");
  await ab("wait", "--text", "No CI projects configured");
  await auditPage([[1440, 1000], [390, 844]]);

  step = "Connector registry error, retry and empty state";
  await failNextRequest("/_/api/connectors");
  await goPage("/connectors", "Connectors");
  await ab("wait", "--text", "Connector registry could not be read");
  await ab("find", "role", "button", "click", "--name", "Retry registry read", "--exact");
  await ab("wait", "--text", "No private hosts connected");
  await auditPage([[1440, 1000], [390, 844]]);

  step = "Sites hostname mapping error and recovery";
  await goPage("/sites", "Sites & deployments");
  await failNextRequest("/_/api/status");
  await ab("find", "role", "button", "click", "--name", "Refresh", "--exact");
  await ab("wait", "--text", "Hostname mappings could not be refreshed");
  assert.equal(await evaluate("document.querySelector('dialog[open]') === null"), true);
  await ab("find", "role", "button", "click", "--name", "Refresh", "--exact");
  await ab("wait", "--fn", "!document.body.innerText.includes('Hostname mappings could not be refreshed')");
  await auditPage([[1440, 1000], [390, 844]]);

  step = "Sites rollback review cancellation and confirmation";
  const liveBeforeUI = (await api("GET", "/_/api/sites")).sites.find((s) => s.name === "blog").live;
  const olderBlog = (await api("GET", "/_/api/sites")).sites.find((s) => s.name === "blog").deployments.find((d) => d.id !== liveBeforeUI);
  assert(olderBlog, "rollback fixture has no retained deployment");
  await ab("find", "role", "button", "click", "--name", `Review activation of ${olderBlog.id.slice(0, 12)} for blog`, "--exact");
  await ab("wait", "--text", "Change the live site build?");
  await ab("press", "Escape");
  assert.equal((await api("GET", "/_/api/sites")).sites.find((s) => s.name === "blog").live, liveBeforeUI, "cancel changed live deployment");
  await ab("find", "role", "button", "click", "--name", `Review activation of ${olderBlog.id.slice(0, 12)} for blog`, "--exact");
  await ab("click", "dialog[open] button:last-child");
  await ab("wait", "--text", "blog now serves");
  assert.equal((await api("GET", "/_/api/sites")).sites.find((s) => s.name === "blog").live, olderBlog.id, "confirmed Sites rollback did not switch the live build");

  step = "Application removal review cancellation and confirmation";
  const appHost = `extra.demo.localhost:${PORT}`;
  await goPage("/applications", "Applications");
  await ab("find", "role", "button", "click", "--name", "Refresh configuration", "--exact");
  await ab("wait", "--text", appHost);
  await ab("find", "role", "button", "click", "--name", `Remove ${appHost}`, "--exact");
  await ab("wait", "--text", "Remove this application hostname?");
  await ab("press", "Escape");
  assert((await api("GET", "/_/api/setup")).routes.some((r) => r.host === appHost), "cancelling removal removed the route");
  await ab("find", "role", "button", "click", "--name", `Remove ${appHost}`, "--exact");
  await ab("click", "dialog[open] button:last-child");
  await ab("wait", "--fn", `![...document.querySelectorAll('#main-content h2')].some((el) => el.textContent.trim() === ${JSON.stringify(appHost)})`);
  assert.equal((await api("GET", "/_/api/setup")).routes.some((r) => r.host === appHost), false, "confirmed removal left the route configured");

  step = "responsive layouts and accessibility";
  const pages = [["/overview", "Overview"], ["/applications", "Applications"], ["/sites", "Sites & deployments"], ["/people", "People & passkeys"], ["/connectors", "Connectors"], ["/activity", "Access activity"], ["/routes", "Routes & hostnames"]];
  for (const [href, title] of pages) {
    await goPage(href, title);
    await auditPage([[1440, 1000], [1024, 900], [390, 844], [320, 800]]);
    if (href === "/applications") {
      await ab("set", "viewport", "1440", "1000");
      await ab("screenshot", join(root, "applications-1440.png"));
      await ab("set", "viewport", "390", "844");
      await ab("screenshot", join(root, "applications-390.png"));
    }
  }
  step = "locking hides protected people and sites";
  await goPage("/people", "People & passkeys");
  await ab("wait", "--text", "new-operator-test@example.test");
  await ab("find", "role", "button", "click", "--name", "Acceptance operator", "--exact");
  await ab("find", "role", "button", "click", "--name", "Lock this tab", "--exact");
  await ab("wait", "--text", "Sign in to open the console");
  assert.equal(await evaluate("document.body.innerText.includes('new-operator-test@example.test')"), false, "locking kept the people list visible");
  assert.equal(await evaluate("(async () => (await fetch('/_/api/people')).status)()"), 401, "anonymous people listing remained available");
  assert.equal(await evaluate("(async () => (await fetch('/_/api/sites')).status)()"), 401, "anonymous site listing remained available");
  assert.equal(await evaluate("(async () => (await fetch('/_/events')).status)()"), 401, "anonymous event stream remained available");
  for (const path of ["/_/status", "/_/api/status", "/_/overview"]) assert.equal(await evaluate(`(async () => (await fetch(${JSON.stringify(path)})).status)()`), 401, `anonymous ${path} remained available`);
  assert.equal(await evaluate("(async () => (await fetch('/_/healthz')).status)()"), 200, "readiness probe unavailable");
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
