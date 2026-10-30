// Redirect addresses: an operator retires a hostname from the console and the
// real edge answers it with a permanent redirect that keeps the page.
// OZAL_BIN=... [OUT=DIR] node scripts/demo/redirect-acceptance.mjs
import { execFile } from "node:child_process";
import { promisify } from "node:util";
import { mkdtemp } from "node:fs/promises";
import { tmpdir } from "node:os";
import { join } from "node:path";
import assert from "node:assert/strict";
import { request } from "node:http";
import { chromium } from "playwright-core";
import { startDemoEdge, PORT, DOMAIN } from "./edge.mjs";
const exec = promisify(execFile);
const binary = process.env.OZAL_BIN, out = process.env.OUT;
if (!binary) throw new Error("Set OZAL_BIN to the built binary");
const root = await mkdtemp(join(tmpdir(), "ozal-redirect-"));
const edge = await startDemoEdge({ binary, dir: join(root, "edge"), adminSocket: true });
const browser = await chromium.launch({ executablePath: process.env.CHROME ?? "/home/saint/.agent-browser/browsers/chrome-156.0.8078.80/chrome", args: ["--no-sandbox"] });
const errors = [];
try {
  const { stdout } = await exec(binary, ["console", "login", "-config", join(edge.dir, "config.json"), "-name", "Redirect check", "-url", edge.consoleURL]);
  const page = await browser.newPage({ viewport: { width: 1280, height: 1000 } });
  page.on("pageerror", (e) => errors.push(e.message));
  await page.goto(stdout.split("\n").find((l) => l.startsWith("http")));
  await page.getByText("Redirect check").first().waitFor();
  await page.goto(edge.consoleURL + "/applications");
  await page.getByRole("button", { name: "Add application" }).first().click();
  await page.getByText("A redirect to another address").click();
  await page.locator("#app-redirect").fill("https://ozal.dev/");
  await page.getByRole("button", { name: "Next" }).click();
  const old = `old.${DOMAIN}:${PORT}`;
  await page.locator("#app-host").fill(old);
  await page.getByRole("button", { name: "Next" }).click();
  await page.getByText("Redirects to ozal.dev").waitFor();
  await page.getByText("Everyone: they are sent to the new address").waitFor();
  await page.getByText("both land on the same page at the new address", { exact: false }).waitFor();
  if (out) await page.screenshot({ path: join(out, "redirect-review.png") });
  await page.getByRole("checkbox").check();
  await page.getByRole("button", { name: "Save application" }).click();
  await page.getByRole("button", { name: "Confirm and save" }).click();
  await page.getByText(`Connect ${old}`).waitFor();
  // node:http, because fetch does not let a caller set Host.
  const res = await new Promise((resolve, reject) => request({ host: "127.0.0.1", port: PORT, path: "/docs/install?lang=es", headers: { Host: old } }, resolve).on("error", reject).end());
  assert.equal(res.statusCode, 301);
  assert.equal(res.headers.location, "https://ozal.dev/docs/install?lang=es");
  await page.goto(edge.consoleURL + "/applications");
  await page.getByText("Redirects to ozal.dev").first().waitFor();
  if (out) await page.screenshot({ path: join(out, "redirect-list.png"), fullPage: true });
  assert.deepEqual(errors, []);
  console.log("PASS redirect: created in the console, answered 301 with the path and query kept");
} finally {
  await browser.close();
  await edge.stop?.();
}
process.exit(0);
