// Regenerates site/public/og.png (the link-preview card) from the built site.
//   (cd site && npm run build) && node scripts/demo/og-shot.mjs CHROME_PATH URL
import { chromium } from "playwright-core";
const [chrome, url = "http://127.0.0.1:4399/"] = process.argv.slice(2);
const browser = await chromium.launch({ executablePath: chrome, args: ["--no-sandbox"] });
const page = await browser.newPage({ viewport: { width: 1440, height: 756 }, reducedMotion: "reduce" });
await page.goto(url, { waitUntil: "networkidle" });
await page.addStyleTag({ content: "*{animation:none!important;transition:none!important}" });
await page.screenshot({ path: new URL("../../site/public/og.png", import.meta.url).pathname });
await browser.close();
