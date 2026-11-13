import { defineConfig } from "vite";
import react from "@vitejs/plugin-react";

export default defineConfig({
  base: "/admin/",
  plugins: [react()],
  build: {
    outDir: "../internal/admin/static",
    emptyOutDir: true,
  },
  server: {
    port: 5173,
    proxy: {
      "/admin/api": "http://127.0.0.1:18888",
      "/metrics": "http://127.0.0.1:18887",
      "/healthz": "http://127.0.0.1:18887",
    },
  },
  test: {
    environment: "jsdom",
    globals: true,
    setupFiles: ["./src/test-setup.ts"],
  },
});
