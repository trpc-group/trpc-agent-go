import react from "@vitejs/plugin-react";
import { defineConfig, loadEnv } from "vite";

export default defineConfig(({ mode }) => {
  const env = loadEnv(mode, process.cwd(), "");
  const proxyTarget = env.VITE_AG_UI_PROXY_TARGET || "http://127.0.0.1:8080";

  return {
    plugins: [react()],
    server: {
      proxy: {
        "/agui": {
          target: proxyTarget,
          changeOrigin: true,
        },
        "/history": {
          target: proxyTarget,
          changeOrigin: true,
        },
      },
    },
  };
});

