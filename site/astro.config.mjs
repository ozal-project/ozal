import { defineConfig } from "astro/config";
import tailwindcss from "@tailwindcss/vite";

// Pure static output: Ozal Pages serves dist/ as files, no runtime.
export default defineConfig({
  site: "https://switchboard.octaviosantacruz.com",
  output: "static",
  build: { inlineStylesheets: "never" },
  // The page reads ../docs (the trackers) at build time; the dev server must be
  // allowed to serve it too.
  vite: { plugins: [tailwindcss()], server: { fs: { allow: [".."] } } },
});
