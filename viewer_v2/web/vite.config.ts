import { defineConfig } from "vite";
import react from "@vitejs/plugin-react";

// 開発中は viewer_v2 serve (既定 127.0.0.1:8080) に API と画像を任せる。
const backend = process.env.VIEWER_BACKEND ?? "http://127.0.0.1:8080";

export default defineConfig({
  plugins: [react()],
  build: {
    // dist/.gitkeep を残すため。古いファイルは npm run build が消す。
    emptyOutDir: false,
  },
  server: {
    proxy: { "/api": backend, "/img": backend },
  },
});
