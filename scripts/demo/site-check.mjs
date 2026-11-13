// Browser regressions for the project page against a built site.
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
// The demo is a matrix of people and apps; choosing a cell explains that answer.
const cell = (page, who, app) => page.locator(`[data-cell][data-who="${who}"][data-app="${app}"]`);
const ask = (page, who, app) => cell(page, who, app).click();

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
      privateDownloads: document.querySelectorAll('a[href*="github.com/ozal-project/ozal/releases"]').length,
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
    assert.equal(await page.locator("[data-cell][data-state]").count(), 16, "every person and app has an answer");
    assert.equal(await cell(page, "ana", "grafana").getAttribute("aria-pressed"), "true", "the explained cell is marked");
    assert.equal(await cell(page, "none", "grafana").getAttribute("data-state"), "again", "no session is sent to sign in, not refused");
    await ask(page, "bob", "grafana"); await decision(page, "domain_not_allowed");
    await ask(page, "ana", "grafana"); await decision(page, "allowed");
    await ask(page, "ana", "billing"); await decision(page, "context_rule_denied");
    await page.locator(".engine-settings > summary").click();
    await page.locator('input[name="bound"]').check(); await decision(page, "allowed");
    assert.equal(await cell(page, "ana", "billing").getAttribute("data-state"), "allowed", "a setting re-answers the whole matrix");
    await page.locator('select[name="method"]').selectOption("oidc");
    assert.equal(await page.locator('input[name="bound"]').isDisabled(), true);
    // The demo Principal records OIDC as not device-bound (known false), so
    // the device condition must still refuse it.
    await decision(page, "context_rule_denied");
    await ask(page, "none", "blog"); await decision(page, "allowed");
    await ask(page, "none", "grafana"); await decision(page, "authentication_required");
    // The tour: on a wide screen one sticky stage shows the step being read;
    // on a narrow one every step keeps its own screen.
    await page.locator("#step-3 .step-copy h3").evaluate((node) => scrollTo(0, node.getBoundingClientRect().top + scrollY - innerHeight / 2));
    await page.locator("#step-3 .crop img").waitFor({ state: "visible" });
    if (width > 860) {
      await page.locator("#step-3.is-active").waitFor();
      assert.equal(await page.locator(".step.is-active").count(), 1, "one step is on stage at a time");
      assert.equal(await page.locator("#step-1 .step-shot").evaluate((node) => getComputedStyle(node).opacity), "0", "the other screens leave the stage");
      assert.equal(await page.locator("#step-1 .crop").isVisible(), false, "hidden screens cannot receive keyboard focus");
      assert.equal(await page.locator("#step-3 .step-shot").evaluate((node) => getComputedStyle(node).position), "sticky");
    } else {
      assert.equal(await page.locator("#step-1 .step-shot").evaluate((node) => getComputedStyle(node).opacity), "1", "a narrow screen shows every step's screen");
    }
    assert.ok(await page.evaluate(() => document.documentElement.scrollWidth <= innerWidth), `${width}px tour overflows`);
    assert.equal(await page.locator(".step").count(), 5, "the tour lists five steps");
    assert.equal(await page.locator("video").count(), 0, "the page carries no video");
    await ask(page, "ana", "grafana");
    await page.evaluate(() => scrollTo(0, 0));
    if (output && [1440, 768, 390].includes(width)) await page.screenshot({ path: join(output, `${width}.png`), fullPage: true });
    await context.close();
    console.log(`PASS ${width}px: geometry, navigation, the tour stage, the matrix and eight engine decisions`);
  }

  const plain = await browser.newContext({ javaScriptEnabled: false, viewport: { width: 390, height: 844 } });
  const staticPage = await plain.newPage();
  await staticPage.goto(url);
  assert.equal(await staticPage.locator("[data-fallback]").isVisible(), true);
  assert.equal(await staticPage.locator("h1").isVisible(), true);
  for (const n of [1, 4, 5]) assert.equal(await staticPage.locator(`#step-${n} .crop img`).isVisible(), true, "every tour step shows its screen without scripts");
  await plain.close();
  console.log("PASS JavaScript disabled: explanation, recorded answer and tour steps");


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
  const moving = await browser.newContext({ viewport: { width: 1440, height: 900 } });
  const motion = await moving.newPage(); checkPage(motion);
  await motion.goto(url);
  await motion.locator("#scene-bob:checked").waitFor({ state: "attached", timeout: 9000 });
  await motion.locator("label:has(#scene-anon)").click();
  await motion.waitForTimeout(6500);
  assert.equal(await motion.locator("#scene-anon").isChecked(), true, "the doorway does not move after the visitor chose a request");
  assert.equal(await motion.locator("#door-anon").isVisible(), true);
  await motion.locator("#step-1 .step-copy").scrollIntoViewIfNeeded();
  const before = await motion.evaluate(() => scrollY);
  await motion.waitForTimeout(2500);
  assert.equal(await motion.evaluate(() => scrollY), before, "the tour never scrolls or advances on its own");
  await moving.close();
  console.log("PASS motion: the doorway plays, stops once the visitor chooses, and the tour waits");
  assert.deepEqual(errors, [], "no uncaught browser errors");
} finally {
  await browser.close();
}
