// Browser regressions for SITE-003/SITE-004 against a built site.
// Uses the same build-time Playwright dependency as the recorded tour.
// node site-check.mjs --url http://127.0.0.1:4321 --chrome /path/to/chrome
import assert from "node:assert/strict";
import { mkdirSync } from "node:fs";
import { join } from "node:path";
import { chromium } from "playwright-core";

const arg = (name, fallback) => {
  const at = process.argv.indexOf(name);
  return at === -1 ? fallback : process.argv[at + 1];
};
const url = arg("--url", "http://127.0.0.1:4321");
const chrome = arg("--chrome", process.env.CHROME);
const output = arg("--out");
if (!chrome) throw new Error("Pass --chrome PATH or set CHROME to an installed Chrome/Chromium executable.");
if (output) mkdirSync(output, { recursive: true });
const browser = await chromium.launch({ executablePath: chrome, args: ["--no-sandbox"] });
const errors = [];
const checkPage = (page) => page.on("pageerror", (error) => errors.push(error.message));
const live = async (page) => page.locator("[data-panel]").waitFor({ state: "visible" });
const decision = async (page, code) => assert.equal(await page.locator("[data-code]").textContent(), code);
const choose = (page, group, value) => page.locator(`label:has(input[name="${group}"][value="${value}"])`).click();

try {
  for (const width of [1440, 1024, 768, 390, 320]) {
    const context = await browser.newContext({ viewport: { width, height: width <= 390 ? 844 : 1000 }, reducedMotion: "reduce" });
    const page = await context.newPage(); checkPage(page);
    await page.goto(url);
    await page.keyboard.press("Tab");
    assert.equal(await page.locator(":focus").textContent(), "Skip to content");
    await page.keyboard.press("Enter");
    assert.equal(await page.evaluate(() => document.activeElement.id), "main-content");
    await page.evaluate(() => scrollTo(0, 0));
    const geometry = await page.evaluate(() => ({
      overflow: document.documentElement.scrollWidth - innerWidth,
      titleOverflow: document.querySelector("h1").scrollWidth - document.querySelector("h1").clientWidth,
      introInView: document.querySelector(".hero-description").getBoundingClientRect().bottom <= innerHeight && document.querySelector(".hero-actions .button").getBoundingClientRect().bottom <= innerHeight,
      navigation: [...document.querySelectorAll("header nav a")].every((node) => {
        const box = node.getBoundingClientRect(); return box.width > 0 && box.left >= 0 && box.right <= innerWidth;
      }),
      brokenAnchors: [...document.querySelectorAll('a[href^="#"]')].filter((link) => !document.getElementById(link.hash.slice(1))).map((link) => link.hash),
      privateDownloads: document.querySelectorAll('a[href*="github.com/octaviosantacruz/switchboard"]').length,
    }));
    assert.ok(geometry.overflow <= 0, `${width}px horizontal overflow: ${geometry.overflow}`);
    assert.ok(geometry.titleOverflow <= 1, `${width}px clipped heading`);
    assert.ok(geometry.introInView, `${width}px explanation and primary action must fit the first screen`);
    assert.ok(geometry.navigation, `${width}px navigation must remain visible and fit`);
    assert.deepEqual(geometry.brokenAnchors, []);
    assert.equal(geometry.privateDownloads, 0, "private releases must not be advertised as a public download");
    await page.locator("#engine").scrollIntoViewIfNeeded();
    await live(page);
    await decision(page, "allowed");
    await choose(page, "who", "bob"); await decision(page, "domain_not_allowed");
    await choose(page, "who", "ana"); await decision(page, "allowed");
    await choose(page, "app", "billing"); await decision(page, "context_rule_denied");
    await page.locator(".engine-settings > summary").click();
    await page.locator('input[name="bound"]').check(); await decision(page, "allowed");
    await page.locator('select[name="method"]').selectOption("oidc");
    assert.equal(await page.locator('input[name="bound"]').isDisabled(), true);
    // The demo Principal records OIDC as not device-bound (known false), so
    // the device condition must still refuse it.
    await decision(page, "context_rule_denied");
    await choose(page, "app", "blog"); await choose(page, "who", "none"); await decision(page, "allowed");
    await choose(page, "app", "grafana"); await decision(page, "authentication_required");
    await page.locator("#walk-1").focus();
    await page.keyboard.press("ArrowRight");
    assert.equal(await page.locator("#walk-2").isChecked(), true, "native walkthrough radios must work from the keyboard");
    assert.equal(await page.locator("#step-2").isVisible(), true);
    assert.equal(await page.locator("#step-1").isVisible(), false, "only the chosen step is shown");
    await page.locator('label[for="walk-3"]').click();
    assert.equal(await page.locator("#step-3").isVisible(), true, "Next moves to the following step");
    assert.ok(await page.evaluate(() => document.documentElement.scrollWidth <= innerWidth), `${width}px walkthrough overflows`);
    await page.locator("label:has(#walk-1)").click();
    assert.equal(await page.locator(".step").count(), 5, "the walkthrough lists five steps");
    assert.equal(await page.locator("video").count(), 0, "the page carries no video");
    await choose(page, "who", "ana");
    await page.evaluate(() => scrollTo(0, 0));
    if (output && [1440, 768, 390].includes(width)) await page.screenshot({ path: join(output, `${width}.png`), fullPage: true });
    await context.close();
    console.log(`PASS ${width}px: geometry, navigation, keyboard walkthrough, and eight engine decisions`);
  }

  const plain = await browser.newContext({ javaScriptEnabled: false, viewport: { width: 390, height: 844 } });
  const staticPage = await plain.newPage();
  await staticPage.goto(url);
  assert.equal(await staticPage.locator("[data-fallback]").isVisible(), true);
  assert.equal(await staticPage.locator("h1").isVisible(), true);
  await staticPage.locator("label:has(#walk-4)").click();
  assert.equal(await staticPage.locator("#step-4").isVisible(), true);
  assert.equal(await staticPage.locator('a[href="/roadmap/"]').first().isVisible(), true);
  await plain.close();
  console.log("PASS JavaScript disabled: explanation, recorded answer and walkthrough steps");

  const roadmap = await browser.newContext({ viewport: { width: 390, height: 844 } });
  const road = await roadmap.newPage(); checkPage(road);
  await road.goto(`${url}/roadmap/`);
  assert.ok((await road.locator(".module").count()) >= 5, "the roadmap lists its modules");
  assert.ok((await road.locator(".shipped-month li").count()) > 10, "the roadmap lists the changelog");
  assert.ok(await road.evaluate(() => document.documentElement.scrollWidth <= innerWidth), "the roadmap overflows a phone");
  await roadmap.close();
  console.log("PASS roadmap page: modules, changelog, phone width");

  const failing = await browser.newContext();
  const retry = await failing.newPage(); checkPage(retry);
  await retry.route("**/engine.wasm", (route) => route.abort());
  await retry.goto(url);
  await retry.locator("#engine").scrollIntoViewIfNeeded();
  await retry.getByRole("button", { name: "Retry the engine" }).waitFor();
  assert.equal(await retry.locator("[data-fallback]").isVisible(), true);
  await retry.unroute("**/engine.wasm");
  await retry.getByRole("button", { name: "Retry the engine" }).click();
  await live(retry); await decision(retry, "allowed");
  await failing.close();
  console.log("PASS failed engine download: readable fallback and successful retry");

  const saving = await browser.newContext();
  await saving.addInitScript(() => Object.defineProperty(navigator, "connection", { value: { saveData: true }, configurable: true }));
  const saver = await saving.newPage(); checkPage(saver);
  let downloads = 0;
  saver.on("request", (request) => { if (new URL(request.url()).pathname === "/engine.wasm") downloads++; });
  await saver.goto(url);
  await saver.getByRole("button", { name: "Load the engine" }).waitFor();
  assert.equal(downloads, 0, "Save-Data must not automatically download WebAssembly");
  await saver.getByRole("button", { name: "Load the engine" }).click();
  await live(saver); assert.equal(downloads, 1);
  await saving.close();
  console.log("PASS Save-Data: download waits for the visitor");
  assert.deepEqual(errors, [], "no uncaught browser errors");
} finally {
  await browser.close();
}
