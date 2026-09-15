import { defineConfig } from "astro/config";

export default defineConfig({
  output: "static",
  outDir: "../../cmd/server/admin-dist",
  trailingSlash: "never",
  vite: { build: { assetsInlineLimit: 4096 } },
});
