import { mergeConfig } from "vite";
import { fileURLToPath } from "node:url";
import reactPreviewConfig from "./vite.react.config.ts";

// Production embeds the same React app used by browser acceptance, but writes
// directly into the Go package consumed by go:embed.
export default mergeConfig(reactPreviewConfig, {
  build: {
    outDir: fileURLToPath(new URL("../../internal/admin/console", import.meta.url)),
    emptyOutDir: true,
  },
});
