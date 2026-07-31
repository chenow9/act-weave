import { defineConfig, loadEnv } from "vite";

export default defineConfig(({ mode }) => {
  const env = loadEnv(mode, ".", "");
  const bffPort = env.BFF_PORT || "8790";
  return {
    root: "client",
    publicDir: "public",
    server: {
      host: "127.0.0.1",
      port: 5188,
      proxy: {
        "/bff": {
          target: `http://127.0.0.1:${bffPort}`,
          changeOrigin: true,
        },
      },
    },
    build: {
      outDir: "../dist",
      emptyOutDir: true,
    },
  };
});
