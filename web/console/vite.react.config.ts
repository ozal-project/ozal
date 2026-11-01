import react from "@vitejs/plugin-react";
import tailwindcss from "@tailwindcss/vite";
import { resolve } from "node:path";
import { fileURLToPath } from "node:url";
import { defineConfig } from "vite";

const consoleCSP = "default-src 'none'; script-src 'self'; style-src 'self'; style-src-attr 'unsafe-inline'; font-src 'self'; img-src 'self' data:; connect-src 'self'; form-action https://github.com; base-uri 'none'; frame-ancestors 'none'";
const adminProxy = { target: process.env.OZAL_ADMIN ?? "http://127.0.0.1:9090", changeOrigin: false };

// Browser acceptance can set a per-run directory without touching Go assets.
export default defineConfig({
  root: fileURLToPath(new URL("./src/react", import.meta.url)),
  publicDir: fileURLToPath(new URL("./public", import.meta.url)),
  plugins: [tailwindcss(), react()],
  resolve: {
    alias: { $lib: fileURLToPath(new URL("./src/lib", import.meta.url)) },
  },
  build: {
    outDir: process.env.OZAL_REACT_OUT_DIR
      ? resolve(process.env.OZAL_REACT_OUT_DIR)
      : fileURLToPath(new URL("../../.local/ui-012/react-dist", import.meta.url)),
    emptyOutDir: true,
    assetsDir: "assets",
    sourcemap: false,
    assetsInlineLimit: 0,
  },
  server: {
    proxy: { "/_/": adminProxy },
  },
  preview: {
    headers: { "Content-Security-Policy": consoleCSP },
    proxy: { "/_/": adminProxy },
  },
});
