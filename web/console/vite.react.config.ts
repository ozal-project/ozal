import react from "@vitejs/plugin-react";
import tailwindcss from "@tailwindcss/vite";
import { fileURLToPath } from "node:url";
import { defineConfig } from "vite";

// The React migration preview builds separately from the embedded Svelte app
// until parity is complete. It never creates a hybrid bundle.
export default defineConfig({
  root: fileURLToPath(new URL("./src/react", import.meta.url)),
  plugins: [tailwindcss(), react()],
  resolve: {
    alias: { $lib: fileURLToPath(new URL("./src/lib", import.meta.url)) },
  },
  build: {
    outDir: fileURLToPath(new URL("../../.local/ui-012/react-dist", import.meta.url)),
    emptyOutDir: true,
    assetsDir: "assets",
    sourcemap: false,
    assetsInlineLimit: 0,
  },
  server: {
    proxy: { "/_/": process.env.OZAL_ADMIN ?? "http://127.0.0.1:9090" },
  },
});
