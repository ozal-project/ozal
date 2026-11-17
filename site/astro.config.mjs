import { defineConfig } from "astro/config";
import tailwindcss from "@tailwindcss/vite";

// Pure static output: Ozal Pages serves dist/ as files, no runtime.
export default defineConfig({
  site: "https://ozal.dev",
  output: "static",
  build: { inlineStylesheets: "never" },
  // Keep page scripts external so the published CSP can forbid inline
  // JavaScript instead of carrying an unsafe-inline exception.
  vite: { plugins: [tailwindcss()], build: { assetsInlineLimit: 0 } },
  // The guides at /docs are the repository's Markdown. Code blocks stay plain,
  // like the page's terminals, with no inline highlight styles.
  markdown: { syntaxHighlight: false },
});
