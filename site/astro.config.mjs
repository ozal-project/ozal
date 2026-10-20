import { defineConfig } from "astro/config";
import tailwindcss from "@tailwindcss/vite";

// Pure static output: Switchboard Pages serves dist/ as files, no runtime.
export default defineConfig({
  site: "https://switchboard.octaviosantacruz.com",
  output: "static",
  build: { inlineStylesheets: "never" },
  vite: { plugins: [tailwindcss()] },
});
