import vue from "@vitejs/plugin-vue";
import { defineConfig } from "vite";

export default defineConfig({
  plugins: [vue()],
  server: {
    host: "127.0.0.1",
    port: 5174,
    proxy: {
      "/api": {
        target: "http://127.0.0.1:8082",
        changeOrigin: true,
        // Long LLM SSE jobs (prompt enhance / smart-dag turns) need no proxy idle cut-off.
        timeout: 0,
        proxyTimeout: 0,
      },
    },
  },
  build: {
    chunkSizeWarningLimit: 1200,
    rollupOptions: {
      onwarn(warning, warn) {
        if (warning.code === "INVALID_ANNOTATION" && warning.id?.includes("@vueuse/core")) {
          return;
        }
        warn(warning);
      },
    },
  },
  test: {
    environment: "jsdom",
    globals: true,
    exclude: ["e2e/**", "node_modules/**", "dist/**"],
  },
});
