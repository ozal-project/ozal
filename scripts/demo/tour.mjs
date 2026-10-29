// Records the product tour and the page screenshots (SITE-004) from a real
// edge with demo data. Nothing is staged: the operator really invites Ana in
// the console, Ana and Bob really create passkeys (a virtual authenticator
// stands in for a phone), and the console really shows who got in. Only the
// captions and the pointer are added, because a screen recording shows neither.
//
//   cd scripts/demo && npm ci
//   node tour.mjs --binary ../../ozal --chrome /path/to/chrome
//
// Output: site/public/tour/tour.webm and site/public/shots/*.png.
import { chromium } from "playwright-core";
import { execFileSync } from "node:child_process";
import { existsSync, mkdirSync, readdirSync, renameSync, rmSync, writeFileSync } from "node:fs";
import { homedir } from "node:os";
import { dirname, join, resolve } from "node:path";
import { fileURLToPath } from "node:url";
import { startDemoEdge, DOMAIN, PORT } from "./edge.mjs";

const here = dirname(fileURLToPath(import.meta.url));
const arg = (flag, fallback) => (process.argv.includes(flag) ? process.argv[process.argv.indexOf(flag) + 1] : fallback);
const binary = resolve(arg("--binary", join(here, "../../ozal")));
const chrome = arg("--chrome", process.env.CHROME);
const site = resolve(here, "../../site/public");
const tourDir = join(site, "tour");
const shotsDir = join(site, "shots");
const dataDir = resolve(here, "../../site/src/data");
const work = join(here, ".recording");
const SIZE = { width: 1280, height: 800 };

const pause = (ms) => new Promise((r) => setTimeout(r, ms));

// The caption bar and the pointer live in the page, so they survive scrolling
// and are recorded with it. They are drawn after every navigation.
const overlay = () => {
  const install = () => {
    if (document.getElementById("tour-caption")) return;
    const bar = document.createElement("div");
    bar.id = "tour-caption";
    bar.style.cssText = "position:fixed;left:50%;bottom:26px;transform:translateX(-50%);z-index:2147483647;max-width:min(880px,92vw);padding:12px 20px;border-radius:999px;background:rgba(17,17,20,.94);color:#ededef;font:500 17px/1.35 system-ui,sans-serif;border:1px solid #34343c;box-shadow:0 12px 40px rgba(0,0,0,.5);opacity:0;transition:opacity .35s;pointer-events:none;text-align:center";
    const dot = document.createElement("div");
    dot.id = "tour-pointer";
    dot.style.cssText = "position:fixed;left:-40px;top:-40px;width:22px;height:22px;margin:-11px 0 0 -11px;border-radius:50%;border:2px solid #c4a17c;background:rgba(196,161,124,.28);z-index:2147483647;pointer-events:none;transition:transform .12s";
    document.documentElement.append(bar, dot);
    addEventListener("mousemove", (e) => { dot.style.left = e.clientX + "px"; dot.style.top = e.clientY + "px"; }, true);
    addEventListener("mousedown", () => (dot.style.transform = "scale(.7)"), true);
    addEventListener("mouseup", () => (dot.style.transform = ""), true);
    const saved = sessionStorage.getItem("tour-caption");
    if (saved) { bar.textContent = saved; bar.style.opacity = "1"; }
  };
  if (document.readyState === "loading") addEventListener("DOMContentLoaded", install); else install();
};

let cleanup = async () => {};
let livePage; // for a screenshot of where a failure happened

async function main() {
  if (!chrome) throw new Error("pass --chrome PATH (or set CHROME): a Chrome or Chromium binary");
  const edge = await startDemoEdge({ binary, adminSocket: true });
  let browser;
  cleanup = async () => { await browser?.close().catch(() => {}); await edge.stop().catch(() => {}); };
  rmSync(work, { recursive: true, force: true });
  mkdirSync(tourDir, { recursive: true });
  mkdirSync(shotsDir, { recursive: true });

  browser = await chromium.launch({ executablePath: chrome, args: ["--no-sandbox", "--hide-scrollbars"] });
  const context = await browser.newContext({ viewport: SIZE, recordVideo: { dir: work, size: SIZE }, bypassCSP: true, deviceScaleFactor: 2 });
  await context.addInitScript(overlay);
  const page = await context.newPage();
  livePage = page;
  const cdp = await context.newCDPSession(page);
  await cdp.send("WebAuthn.enable");
  await cdp.send("WebAuthn.addVirtualAuthenticator", { options: { protocol: "ctap2", transport: "internal", hasResidentKey: true, hasUserVerification: true, isUserVerified: true, automaticPresenceSimulation: true } });

  const say = async (text) => {
    await page.evaluate((t) => {
      sessionStorage.setItem("tour-caption", t);
      const bar = document.getElementById("tour-caption");
      if (bar) { bar.textContent = t; bar.style.opacity = t ? "1" : "0"; }
    }, text);
  };
  const go = async (url, caption) => {
    await page.goto(url, { waitUntil: "load" });
    await page.waitForTimeout(500);
    if (caption !== undefined) await say(caption);
  };
  const move = async (locator) => {
    const box = await locator.boundingBox();
    if (box) await page.mouse.move(box.x + box.width / 2, box.y + box.height / 2, { steps: 28 });
  };
  const click = async (locator) => { await move(locator); await pause(250); await locator.click(); };
  const type = async (locator, text) => { await click(locator); await locator.pressSequentially(text, { delay: 60 }); };
  // Stills are JPEG at twice the size: they are cropped into tiles on the page.
  // The caption bar and the pointer belong to the film, not to the product: hide them for a still.
  const overlayVisible = (visible) => page.evaluate((v) => { for (const id of ["tour-caption", "tour-pointer"]) document.getElementById(id)?.style.setProperty("visibility", v ? "visible" : "hidden"); }, visible);
  const shot = async (name, target) => {
    await overlayVisible(false);
    await (target ?? page).screenshot({ path: join(shotsDir, `${name}.jpg`), type: "jpeg", quality: 88 });
    await overlayVisible(true);
  };
  // The console opens only after sign-in, and its session lives in the tab: a
  // host login (as the host's operator would use) signs in, and the tour moves
  // between console pages with the menu instead of reloading.
  const signInConsole = async (caption) => {
    const out = execFileSync(binary, ["console", "login", "-config", join(edge.dir, "config.json"), "-name", "Operator", "-url", edge.consoleURL], { encoding: "utf8" });
    const url = out.split("\n").find((line) => line.startsWith("http"));
    if (!url) throw new Error("host login returned no URL");
    await go(url, caption);
    await page.waitForSelector('nav[aria-label="Console"]');
  };
  const open = async (label) => {
    await click(page.locator('nav[aria-label="Console"] a, nav[aria-label="Console settings"] a', { hasText: label }));
    await page.waitForFunction((l) => document.querySelector("#main-content h1")?.textContent?.trim() === l, label);
  };
  const t0 = Date.now();
  const marks = {};
  const mark = (name) => { marks[name] = (Date.now() - t0) / 1000; };

  // 1 · The operator opens the console.
  await signInConsole("The operator opens the console. One thing needs attention.");
  await pause(2500);
  await move(page.locator("text=is not answering").first());
  await pause(2200);

  // 2 · Invite Ana.
  mark("invite");
  await say("Inviting Ana: a one-time link, no password to share.");
  await open("People");
  await click(page.getByRole("button", { name: "Invite person" }));
  await page.waitForSelector("#inv-email");
  await type(page.locator("#inv-email"), "ana@acme.dev");
  await type(page.locator("#inv-name"), "Ana");
  await click(page.getByRole("button", { name: "Create invitation" }));
  const link = await page.locator('input[aria-label="Invitation link"]').inputValue();
  await pause(1800);
  await click(page.getByRole("button", { name: "Done" }));

  // 3 · Ana creates her passkey.
  await go(link, "Ana opens her link and creates a passkey on her own device.");
  await pause(1800);
  await click(page.locator("#action"));
  await page.waitForSelector("#who:not([hidden])", { timeout: 20000 });
  await say("Her page lists only the apps she may open.");
  await pause(1500);
  await shot("portal", page.locator("main"));
  await pause(2200);
  await click(page.locator("#apps a", { hasText: "grafana" }));
  await page.waitForLoadState("load");
  await say("She opens Grafana. Straight in.");
  await pause(2200);
  for (const [app, caption] of [["billing", "Billing opens too: her passkey stays on one device, as that app requires."], ["wiki", "The wiki may ask her to sign in again if her session is old."]]) {
    await go(`http://auth.${DOMAIN}:${PORT}/_/auth/me`, "Back to her apps.");
    await pause(900);
    await click(page.locator("#apps a", { hasText: app }));
    await page.waitForLoadState("load");
    await say(caption);
    await pause(2000);
  }

  // 4 · Bob, from another company, signs up with his own invitation.
  await go(`http://auth.${DOMAIN}:${PORT}/_/auth/me`, "Back to her apps. She signs out.");
  await pause(1200);
  // The virtual authenticator holds Ana's passkey, so a real sign-in page would sign her straight
  // back in and race Bob's invitation. Sign out for real, but let the sign-in page stay blank.
  const signIn = /\/_\/auth\/login(\?|$)/;
  await page.route(signIn, (route) => route.fulfill({ contentType: "text/html", body: "<!doctype html><title>Signed out</title>" }));
  await click(page.getByText("Sign out", { exact: true }).first());
  await page.waitForURL(signIn);
  await page.unroute(signIn);
  await go(edge.invites.bob, "Bob, from another company, creates his passkey.");
  await pause(1500);
  await click(page.locator("#action"));
  await page.waitForSelector("#who:not([hidden])", { timeout: 20000 });
  await say("Nothing is open to Bob, and his page says so.");
  await pause(3000);
  await go(edge.appURL("grafana"), "He tries Grafana anyway: a clear answer and a way back, not a bare error.");
  await shot("refused", page.locator("main"));
  await pause(3600);
  for (const app of ["wiki", "billing"]) await go(edge.appURL(app), undefined);
  await pause(600);

  // 5 · The operator sees who got in, and why Bob did not.
  await signInConsole("Back in the console: who got in, and why Bob was refused.");
  await pause(2800);
  await move(page.locator("text=Why people were refused"));
  await shot("overview");
  await pause(3400);
  await open("Activity");
  await page.locator("table").getByText("Access denied").first().waitFor();
  await click(page.locator("button", { hasText: "All events" }).first());
  await click(page.getByRole("option", { name: "Access decisions" }));
  await say("Every decision, with its reason.");
  await shot("access-log");
  await exportAccessSample(edge);
  await pause(3600);

  // 6 · A deploy lands while the operator watches.
  await open("Overview");
  await page.waitForSelector("text=Recent changes");
  await say("A deploy lands. The console shows it, and where it came from.");
  await pause(1500);
  await edge.deploy("blog", { "index.html": "<!doctype html><meta charset=utf-8><title>Acme blog</title><h1>Acme blog</h1><p>Launch day.</p>" });
  await page.waitForSelector("text=blog was updated");
  await pause(3800);
  await say("Ozal: every door to your apps, one place that decides.");
  await open("People");
  await shot("people");
  await pause(3400);
  await say("");
  await pause(600);

  const video = page.video();
  await context.close();
  const tour = join(tourDir, "tour.webm");
  renameSync(await video.path(), tour);
  rmSync(work, { recursive: true, force: true });
  cutLoop(tour, join(tourDir, "loop.webm"), marks.invite ?? 12);
  await cleanup();
  console.log(`tour recorded: ${tour}\nshots: ${shotsDir}\nsample: ${join(dataDir, "access-sample.json")}`);
}

// The access log rows on the project page are this run's own events, with the
// opaque passkey id replaced by the person's email like the console does.
async function exportAccessSample(edge) {
  const people = await (await fetch(`${edge.consoleURL}/_/api/people`, { headers: { Authorization: `Bearer ${edge.token}` } })).json();
  const subjects = new Map((people.users ?? []).map((u) => [u.subject, u.email]));
  const controller = new AbortController();
  const response = await fetch(`${edge.consoleURL}/_/events`, { headers: { Authorization: `Bearer ${edge.token}` }, signal: controller.signal });
  const reader = response.body.getReader();
  const decoder = new TextDecoder();
  let text = "";
  const until = Date.now() + 1500;
  while (Date.now() < until) {
    const chunk = await Promise.race([reader.read(), new Promise((r) => setTimeout(() => r({ done: true }), 400))]);
    if (chunk.done) break;
    text += decoder.decode(chunk.value);
  }
  controller.abort();
  const events = [...text.matchAll(/^data: (.+)$/gm)].map((m) => JSON.parse(m[1]));
  const rows = events
    .filter((e) => e.type === "access.allowed" || e.type === "access.denied")
    .map((e) => ({ time: e.time, who: subjects.get(e.actor) ?? "someone", host: e.host.replace(`.${DOMAIN}:${PORT}`, ""), type: e.type, code: e.code ?? "", reason: e.reason ?? "" }))
    .sort((a, b) => a.time.localeCompare(b.time))
    // A browser asks for the page and then for /favicon.ico: one decision each, one row here.
    .filter((row, i, all) => i === 0 || ["who", "host", "type"].some((k) => row[k] !== all[i - 1][k]) || Date.parse(row.time) - Date.parse(all[i - 1].time) > 2000);
  mkdirSync(dataDir, { recursive: true });
  writeFileSync(join(dataDir, "access-sample.json"), JSON.stringify(rows, null, 2) + "\n");
}

// A few seconds of the console in use, looped, small enough to autoplay muted.
function cutLoop(source, target, from) {
  const found = readdirSync(join(homedir(), ".cache", "ms-playwright")).find((d) => d.startsWith("ffmpeg-"));
  const ffmpeg = process.env.FFMPEG ?? (found && join(homedir(), ".cache", "ms-playwright", found, "ffmpeg-linux"));
  if (!ffmpeg || !existsSync(ffmpeg)) return console.warn("no ffmpeg: the loop clip was not cut (set FFMPEG, or run: npx playwright install ffmpeg)");
  execFileSync(ffmpeg, ["-y", "-loglevel", "error", "-ss", String(from), "-t", "9", "-i", source, "-an", "-c:v", "libvpx-vp9", "-b:v", "800k", "-deadline", "good", "-cpu-used", "4", "-vf", "scale=1280:-2", target]);
}

main().then(() => process.exit(0), async (error) => {
  console.error(error);
  await livePage?.screenshot({ path: join(here, "failure.png") }).then(() => console.error("where it stopped: scripts/demo/failure.png")).catch(() => {});
  await cleanup();
  process.exit(1);
});
