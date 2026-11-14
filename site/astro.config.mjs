import { defineConfig } from "astro/config";
import tailwindcss from "@tailwindcss/vite";

// Pure static output: Ozal Pages serves dist/ as files, no runtime.
export default defineConfig({
  site: "https://ozal.dev",
  output: "static",
  build: { inlineStylesheets: "never" },
  vite: { plugins: [tailwindcss()] },
  // The guides at /docs are the repository's Markdown. Code blocks stay plain,
  // like the page's terminals, with no inline highlight styles.
  markdown: { syntaxHighlight: false },
});
