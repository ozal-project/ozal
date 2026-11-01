// Browser acceptance for the durable access history (UI-005) against an
// isolated real edge that audits access decisions: Overview's "Who got in"
// reads the journal, updates as decisions arrive, and keeps its counts after
// the edge restarts.
//
//   OZAL_BIN=/path/to/ozal CHROME=/path/to/chrome node scripts/demo/access-history-acceptance.mjs
import assert from "node:assert/strict";
import { execFile } from "node:child_process";
import { mkdtemp, rm } from "node:fs/promises";
import { tmpdir } from "node:os";
import { join } from "node:path";
import { promisify } from "node:util";
import { chromium } from "playwright-core";
import { startDemoEdge } from "./edge.mjs";

const exec = promisify(execFile);
const binary = process.env.OZAL_BIN;
const chrome = process.env.CHROME;
if (!binary || !chrome) throw new Error("Set OZAL_BIN and CHROME");
const root = await mkdtemp(join(tmpdir(), "sb-access-"));
let edge, browser, step = "start";

// A host login: the operator session lives in the edge's memory, so a
// restarted edge needs a new one.
const signIn = async (page) => {
  const { stdout } = await exec(binary, ["console", "login", "-config", join(edge.dir, "config.json"), "-name", "History check", "-url", edge.consoleURL]);
  const url = stdout.split("\n").find((line) => line.startsWith("http"));
  assert(url, "host login returned no URL");
  await page.goto(url);
  await page.getByRole("heading", { name: "Who got in" }).waitFor();
};
const access = (page) => page.locator("section[aria-labelledby=overview-access]");
// The counts, in the order the section lists them: let in, refused, asked again.
const counts = async (page) => (await access(page).locator("dd").allTextContents()).map(Number);
// A visitor: a browser with its own virtual authenticator that creates a
// passkey from the invitation and then visits apps through the edge.
const visitor = async (invitation) => {
  const context = await browser.newContext();
  const page = await context.newPage();
  const cdp = await context.newCDPSession(page);
  await cdp.send("WebAuthn.enable");
  await cdp.send("WebAuthn.addVirtualAuthenticator", { options: { protocol: "ctap2", transport: "internal", hasResidentKey: true, hasUserVerification: true, isUserVerified: true, automaticPresenceSimulation: true } });
  await page.goto(invitation);
  await page.locator("#action").click();
  await page.waitForSelector("#who:not([hidden])", { timeout: 20_000 });
  return page;
};
const api = async () => {
  const res = await fetch(`${edge.consoleURL}/_/api/access?limit=1`, { headers: { Authorization: `Bearer ${edge.token}` } });
  assert.equal(res.status, 200, "access history API");
  return (await res.json()).summary;
};

try {
  edge = await startDemoEdge({ binary, dir: join(root, "edge"), adminSocket: true, auditAccess: true });
  browser = await chromium.launch({ executablePath: chrome, args: ["--no-sandbox"] });
  const page = await (await browser.newContext({ viewport: { width: 1280, height: 900 } })).newPage();

  step = "an empty record says so";
  await signIn(page);
  await access(page).getByText("The durable access record has no decisions yet.").waitFor();

  step = "visitors with passkeys reach and are refused by Grafana";
  const carla = await visitor(edge.invites.carla);
  const bob = await visitor(edge.invites.bob);
  await carla.goto(edge.appURL("grafana"));
  await bob.goto(edge.appURL("grafana"));

  step = "Overview reads the durable record";
  await page.reload();
  await access(page).getByText("from the durable access record").waitFor();
  const before = await api();
  assert.equal(before.source, "journal");
  assert(before.allowed >= 1 && before.refused >= 1, `summary ${JSON.stringify(before)}`);
  assert.deepEqual((await counts(page)).slice(0, 2), [before.allowed, before.refused]);
  await access(page).getByText("carla@acme.dev").waitFor();
  await access(page).getByText("Email domain not allowed").waitFor();

  step = "a new decision reaches the counts";
  await bob.goto(edge.appURL("billing"));
  await page.waitForFunction((want) => {
    const dd = document.querySelectorAll("section[aria-labelledby=overview-access] dd");
    return Number(dd[1]?.textContent) > want;
  }, before.refused, { timeout: 15_000 });

  step = "the counts survive a restart";
  const beforeRestart = await api();
  await edge.restart();
  await signIn(page);
  await access(page).getByText("from the durable access record").waitFor();
  const after = await api();
  assert.equal(after.allowed, beforeRestart.allowed, "allowed decisions before the restart were lost");
  assert.equal(after.refused, beforeRestart.refused, "refusals before the restart were lost");
  assert.equal(after.since, beforeRestart.since, "the history's start moved across the restart");
  assert.deepEqual((await counts(page)).slice(0, 2), [after.allowed, after.refused]);
  await access(page).getByText("carla@acme.dev").waitFor();

  console.log("Access history: Overview reads the durable journal (empty, then real passkey visitors let in and refused), updates on new decisions, and keeps counts, people and start across an edge restart");
} catch (e) {
  console.error(`access history acceptance failed at: ${step}`);
  throw e;
} finally {
  await browser?.close();
  await edge?.stop();
  await rm(root, { recursive: true, force: true });
}
