// Browser acceptance for the isolated React build against a disposable local
// edge. This is preview coverage; it does not replace real-edge passkey or
// update acceptance before UI-012 cutover.
import assert from "node:assert/strict";
import { execFile } from "node:child_process";
import { promisify } from "node:util";
import { mkdtemp, rm } from "node:fs/promises";
import { tmpdir } from "node:os";
import { join, resolve } from "node:path";
import { createServer } from "node:net";
import { spawn } from "node:child_process";
import { startDemoEdge } from "./edge.mjs";

const exec = promisify(execFile);
const binary = process.env.OZAL_BIN && resolve(process.env.OZAL_BIN);
if (!binary) throw new Error("Set OZAL_BIN to the built Ozal binary");

const root = await mkdtemp(join(tmpdir(), "ozal-react-console-"));
const session = `ozal-react-ui012-${process.pid}`;
const consoleCSP = "default-src 'none'; script-src 'self'; style-src 'self'; style-src-attr 'unsafe-inline'; font-src 'self'; img-src 'self' data:; connect-src 'self'; form-action https://github.com; base-uri 'none'; frame-ancestors 'none'";
let edge;
let preview;
let step = "start";

async function freePort() {
  const socket = createServer();
  await new Promise((ok, fail) => socket.once("error", fail).listen(0, "127.0.0.1", ok));
  const { port } = socket.address();
  await new Promise((ok, fail) => socket.close((error) => error ? fail(error) : ok()));
  return port;
}

async function browser(...args) {
  try {
    const { stdout } = await exec("agent-browser", ["--session", session, "--args", process.env.OZAL_BROWSER_ARGS ?? "--no-sandbox", "--json", ...args], {
      env: { ...process.env, XDG_RUNTIME_DIR: root },
      maxBuffer: 4 << 20,
      timeout: 20_000,
    });
    return stdout;
  } catch (error) {
    const detail = String(error.stderr || error.stdout || error.message).replace(/operator=[^\s"<>]+/g, "operator=[redacted]").slice(0, 1800);
    throw new Error(`Browser ${args[0]} failed during ${step}: ${detail}`);
  }
}

async function evaluate(source) {
  const response = JSON.parse(await browser("eval", source));
  return response.data?.result;
}

async function waitForPreview(url) {
  for (let attempt = 0; attempt < 100; attempt++) {
    if (preview.exitCode !== null) throw new Error("React preview exited before it was ready");
    try {
      const response = await fetch(url);
      if (response.ok) return;
    } catch { /* Vite is still starting. */ }
    await new Promise((ok) => setTimeout(ok, 100));
  }
  throw new Error("React preview did not become ready");
}

async function audit(width, height) {
  await browser("set", "viewport", String(width), String(height));
  const size = await evaluate("({viewport: innerWidth, document: document.documentElement.scrollWidth, body: document.body.scrollWidth, offenders: [...document.querySelectorAll('body *')].filter((el) => { const rect = el.getBoundingClientRect(); const scroller = el.closest('.overflow-x-auto, nav[aria-label$=\" views\"]'); return !scroller && (rect.right > innerWidth + 1 || rect.left < -1); }).slice(0, 12).map((el) => ({tag: el.tagName, className: String(el.className).slice(0, 100), text: (el.textContent || '').trim().slice(0, 80), left: Math.round(el.getBoundingClientRect().left), right: Math.round(el.getBoundingClientRect().right)}))})");
  assert(size.document <= width && size.body <= width, `horizontal overflow at ${width}px: ${JSON.stringify(size)}`);
  const result = JSON.parse(await browser("a11y"));
  const violations = result.data?.violations ?? result.data?.results?.violations;
  assert(Array.isArray(violations), `axe returned no violation list at ${width}px`);
  assert.equal(violations.length, 0, `axe violations at ${width}px: ${JSON.stringify(violations.map((item) => ({ id: item.id, impact: item.impact, help: item.help, targets: item.nodes?.map((node) => node.target) })))}`);
}

try {
  step = "build isolated React preview";
  await exec("npm", ["run", "react:build"], { cwd: join(process.cwd(), "web/console"), maxBuffer: 4 << 20 });
  edge = await startDemoEdge({ binary, dir: join(root, "edge"), adminSocket: true });

  const port = await freePort();
  const previewURL = `http://127.0.0.1:${port}`;
  preview = spawn(process.execPath, [join(process.cwd(), "web/console/node_modules/vite/bin/vite.js"), "preview", "--config", "vite.react.config.ts", "--host", "127.0.0.1", "--port", String(port), "--strictPort"], {
    cwd: join(process.cwd(), "web/console"),
    env: { ...process.env, OZAL_ADMIN: edge.consoleURL },
    stdio: ["ignore", "pipe", "pipe"],
  });
  preview.stdout.resume();
  preview.stderr.resume();
  await waitForPreview(previewURL);

  step = "verify production preview headers and API proxy";
  const [pageResponse, edgeResponse, apiResponse] = await Promise.all([
    fetch(previewURL), fetch(edge.consoleURL), fetch(`${previewURL}/_/healthz`),
  ]);
  assert.equal(pageResponse.headers.get("content-security-policy"), consoleCSP, "React preview CSP differs from the embedded console contract");
  assert.equal(edgeResponse.headers.get("content-security-policy"), consoleCSP, "preview CSP fixture differs from the live console contract");
  assert.equal(apiResponse.status, 200, "React preview did not proxy admin API requests to the local edge");
  const html = await pageResponse.text();
  const metaCSP = html.match(/<meta\s+http-equiv="Content-Security-Policy"\s+content="([^"]+)"\s*\/>/i)?.[1];
  assert.equal(metaCSP, consoleCSP, "React document CSP differs from the embedded console contract");
  const inlineScripts = [...html.matchAll(/<script\b([^>]*)>([\s\S]*?)<\/script>/gi)].filter(([, attributes, body]) => !/\bsrc\s*=/i.test(attributes) && body.trim());
  assert.equal(inlineScripts.length, 0, "React preview contains an inline script");

  step = "host-issued operator sign-in";
  const { stdout } = await exec(binary, ["console", "login", "-config", join(edge.dir, "config.json"), "-name", "React preview acceptance", "-url", previewURL]);
  const loginURL = stdout.split("\n").find((line) => line.startsWith("http"));
  assert(loginURL, "host login returned no one-use URL");

  step = "reduced-motion preference disables sign-in animation";
  await browser("open", previewURL);
  await browser("wait", "--fn", "document.querySelector('.auth-shell') !== null");
  assert.equal(await evaluate("getComputedStyle(document.querySelector('.auth-shell'), '::before').animationName"), "auth-glow", "sign-in animation fixture was not active without reduced motion");
  await browser("set", "media", "light", "reduced-motion");
  assert.equal(await evaluate("matchMedia('(prefers-reduced-motion: reduce)').matches"), true, "browser did not enable reduced motion");
  assert.equal(await evaluate("getComputedStyle(document.querySelector('.auth-shell'), '::before').animationName"), "none", "sign-in animation remained active with reduced motion");
  await browser("set", "media", "light", "no-preference");

  await browser("open", loginURL);
  await browser("wait", "--text", "React preview acceptance");
  assert.equal(await evaluate("location.hash"), "", "one-use credential remained in the URL");
  await browser("wait", "--fn", "document.querySelector('#main-content h1')?.textContent?.trim() === 'Overview'");

  step = "all React page routes and responsive accessibility";
  const pages = [
    ["/overview", "Overview"], ["/activity", "Activity"], ["/routes", "Hostnames"],
    ["/applications", "Applications"], ["/domains", "Domains"], ["/integrations", "Integrations"],
    ["/administrators", "Administrators"], ["/groups", "People"], ["/people", "People"],
    ["/connectors", "Connectors"], ["/sites", "Sites"],
  ];
  for (const [path, heading] of pages) {
    step = `React route ${path}`;
    await browser("open", `${previewURL}${path}`);
    await browser("wait", "--fn", `location.pathname === ${JSON.stringify(path)} && document.querySelector('#main-content h1')?.textContent?.trim() === ${JSON.stringify(heading)}`);
    const placeholder = await evaluate("document.body.innerText.includes('Migration in progress')");
    assert.equal(placeholder, false, `${path} rendered a migration placeholder instead of its React page`);
    for (const [width, height] of [[1440, 1000], [1024, 900], [390, 844], [320, 800]]) {
      step = `React route ${path} at ${width}px`;
      await audit(width, height);
    }
  }

  step = "legacy route aliases";
  for (const [path, destination, heading] of [["/projects", "/sites", "Sites"], ["/simulator", "/activity", "Activity"]]) {
    await browser("open", `${previewURL}${path}`);
    await browser("wait", "--fn", `location.pathname === ${JSON.stringify(destination)} && document.querySelector('#main-content h1')?.textContent?.trim() === ${JSON.stringify(heading)}`);
  }

  step = "unknown route returns to Overview without dropping URL context";
  await browser("open", `${previewURL}/not-a-console-page?source=bookmark#main-content`);
  await browser("wait", "--fn", "location.pathname === '/overview' && document.querySelector('#main-content h1')?.textContent?.trim() === 'Overview'");
  assert.equal(await evaluate("location.search"), "?source=bookmark", "unknown-route redirect dropped the query string");
  assert.equal(await evaluate("location.hash"), "#main-content", "unknown-route redirect dropped the fragment");

  step = "React session resumes after a reload";
  await browser("open", `${previewURL}/people`);
  await browser("wait", "--fn", "document.querySelector('#main-content h1')?.textContent?.trim() === 'People'");
  const sessionName = await evaluate("document.querySelector('.console-account-name')?.textContent?.trim()");
  assert.equal(sessionName, "React preview acceptance", "the operator session did not resume after navigating to a new document");

  step = "keyboard navigation, Escape and focus return in mobile drawer";
  await browser("set", "viewport", "390", "844");
  await browser("find", "role", "button", "click", "--name", "Open menu", "--exact");
  await browser("wait", "--fn", "document.querySelector('#mobile-menu')?.open === true && document.activeElement?.closest('#mobile-menu') !== null");
  await browser("eval", "document.querySelector('#mobile-menu a[href=\"/overview\"]').focus()");
  await browser("press", "Shift+Tab");
  assert.equal(await evaluate("document.activeElement?.getAttribute('aria-label')"), "React preview acceptance", "backward Tab escaped the mobile navigation");
  await browser("press", "Tab");
  assert.equal(await evaluate("document.activeElement?.getAttribute('href')"), "/overview", "Tab did not wrap inside the mobile navigation");
  await browser("press", "Escape");
  await browser("wait", "--fn", "document.querySelector('#mobile-menu')?.open === false && document.activeElement?.getAttribute('aria-label') === 'Open menu'");
  await browser("find", "role", "button", "click", "--name", "Open menu", "--exact");
  await browser("click", "#mobile-menu a[href=\"/sites\"]");
  await browser("wait", "--fn", "location.pathname === '/sites' && !document.querySelector('#mobile-menu')?.open && document.activeElement?.textContent?.trim() === 'Sites'");

  step = "account menu Escape closes and restores focus";
  await browser("set", "viewport", "1440", "1000");
  await browser("find", "role", "button", "click", "--name", "React preview acceptance", "--exact");
  await browser("wait", "--fn", "document.querySelector('[aria-label=\"React preview acceptance\"]')?.getAttribute('aria-expanded') === 'true'");
  await browser("press", "Escape");
  await browser("wait", "--fn", "document.querySelector('[aria-label=\"React preview acceptance\"]')?.getAttribute('aria-expanded') === 'false' && document.activeElement?.getAttribute('aria-label') === 'React preview acceptance'");
  console.log("React preview acceptance passed: production build, CSP parity, local-edge API proxy, host-issued login, reduced-motion behavior, 11 page routes, two aliases, unknown-route fallback, session resume, axe/overflow at 1440/1024/390/320px, and shell keyboard focus behavior.");
} catch (error) {
  console.error(`React preview acceptance failed during ${step}: ${error.message}`);
  try {
    const trace = JSON.parse(await browser("network", "requests", "--filter", "/_/api/operators/exchange"));
    const requests = (trace.data?.requests ?? []).map(({ method, url, status }) => ({ method, url, status }));
    console.error(JSON.stringify(requests));
  } catch { /* No browser network trace. */ }
  try { console.error(await browser("snapshot", "-i")); } catch { /* Browser may not have started. */ }
  process.exitCode = 1;
} finally {
  if (preview && preview.exitCode === null) {
    await new Promise((resolveExit) => {
      const finish = () => { clearTimeout(timer); resolveExit(); };
      const timer = setTimeout(() => { preview.kill("SIGKILL"); resolveExit(); }, 5000);
      preview.once("exit", finish);
      preview.once("error", finish);
      if (preview.exitCode === null) preview.kill("SIGTERM");
      else finish();
    });
  }
  await browser("close").catch(() => {});
  await edge?.stop();
  await rm(root, { recursive: true, force: true });
}
