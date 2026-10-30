// EDGE-009 browser check: the Domains page with zones and the provider
// checklist (zones and setup answers are mocked; the console and edge are real).
// OZAL_BIN=... OUT=DIR node scripts/demo/domains-acceptance.mjs
import { execFile } from "node:child_process";
import { promisify } from "node:util";
import { mkdtemp } from "node:fs/promises";
import { tmpdir } from "node:os";
import { join } from "node:path";
import { chromium } from "playwright-core";
import { startDemoEdge } from "./edge.mjs";
const exec = promisify(execFile);
const binary = process.env.OZAL_BIN, out = process.env.OUT;
const root = await mkdtemp(join(tmpdir(), "ozal-domains-"));
const edge = await startDemoEdge({ binary, dir: join(root, "edge"), adminSocket: true });
const browser = await chromium.launch({ executablePath: "/home/saint/.agent-browser/browsers/chrome-156.0.8078.80/chrome", args: ["--no-sandbox"] });
const errors = [];
try {
  const { stdout } = await exec(binary, ["console", "login", "-config", join(edge.dir, "config.json"), "-name", "Check", "-url", edge.consoleURL]);
  const url = stdout.split("\n").find((l) => l.startsWith("http"));
  const page = await browser.newPage({ viewport: { width: 1440, height: 1100 } });
  page.on("pageerror", (e) => errors.push(e.message));
  page.on("console", (m) => { if (m.type() === "error") errors.push(m.text()); });
  const zone = (name, state, extra = {}) => ({ name, source: "runtime", state, names: [name, "*." + name], challenge: { name: "_acme-challenge." + name, target: name.replace(".", "-") + ".acme.ozal.dev", delegated: true }, hosts: [name], ...extra });
  await page.route("**/_/api/zones", (r) => r.fulfill({ json: { config_sha256: "d", available: true, setup: true, provider: "cloudflare", zones: [zone("ozal.dev", "active", { not_after: "2027-01-28T00:00:00Z", renews_at: "2026-12-29T00:00:00Z" }), zone("octaviosantacruz.com", "active", { not_after: "2027-01-28T00:00:00Z" })] } }));
  let applied = false;
  const report = (zoneName) => ({ zone: zoneName, provider: "cloudflare", ready: applied || zoneName !== "ozal.dev", steps: [
    { name: "challenge", state: "done", title: "DNS record for certificates", detail: "In place." },
    { name: "encryption", state: applied || zoneName !== "ozal.dev" ? "done" : "todo", action: "set_strict", title: "Encrypted connection from the proxy", detail: "Full (strict).", manual: "SSL/TLS → Overview: choose Full (strict)." },
    { name: "origin_pull", state: applied || zoneName !== "ozal.dev" ? "done" : "todo", action: "upload_certificate", title: "Proxy certificate for this edge", detail: "The proxy has no certificate this edge trusts: visitors get error 520.", manual: "…" } ] });
  await page.route("**/_/api/zones/*/setup", (r) => {
    const name = decodeURIComponent(r.request().url().split("/zones/")[1].split("/")[0]);
    if (r.request().method() === "POST") { applied = true; return r.fulfill({ json: { ...report(name), applied: ["set_strict", "upload_certificate"] } }); }
    return r.fulfill({ json: report(name) });
  });
  await page.goto(url);
  await page.getByText("Check").first().waitFor();
  await page.goto(edge.consoleURL + "/domains");
  await page.getByText("Finish the setup at your DNS provider").waitFor({ timeout: 10000 });
  await page.screenshot({ path: out + "/domains-before.png", fullPage: true });
  await page.getByRole("button", { name: "Set up in Cloudflare" }).click();
  await page.getByText("Done: set encryption to Full (strict), uploaded this edge's proxy certificate.").waitFor();
  await page.screenshot({ path: out + "/domains-after.png", fullPage: true });
  console.log("PASS domains page renders, applies and reports");
} finally {
  await browser.close(); await edge.stop?.();
  console.log("errors:", JSON.stringify(errors));
}
process.exit(0);
