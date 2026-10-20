import { defineConfig } from "vite";
import { svelte } from "@sveltejs/vite-plugin-svelte";
import tailwindcss from "@tailwindcss/vite";
import { fileURLToPath } from "node:url";

// The build lands inside the Go package so `go build` embeds it: Node is a
// build-time tool only and the binary stays self-contained.
export default defineConfig({
  plugins: [tailwindcss(), svelte()],
  resolve: { alias: { $lib: fileURLToPath(new URL("./src/lib", import.meta.url)) } },
  build: {
    outDir: fileURLToPath(new URL("../../internal/admin/console", import.meta.url)),
    emptyOutDir: true,
    assetsDir: "assets",
    sourcemap: false,
  },
  // `npm run dev` proxies the admin API of a local Switchboard.
  server: { proxy: { "/_/": process.env.SWITCHBOARD_ADMIN ?? "http://127.0.0.1:9090" } },
});
