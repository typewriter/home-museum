import { defineConfig, type Plugin } from "vite";
import react from "@vitejs/plugin-react";

// 開発中は viewer_v2 serve (既定 127.0.0.1:8080) に API と画像を任せる。
const backend = process.env.VIEWER_BACKEND ?? "http://127.0.0.1:8080";

// adminPages は開発サーバーで /admin 以下に admin.html を返す (本番は spa.go が返す)。
const adminPages = (): Plugin => ({
  name: "admin-pages",
  configureServer(server) {
    server.middlewares.use((req, _res, next) => {
      if (req.url && /^\/admin(\/|\?|$)/.test(req.url)) req.url = "/admin.html";
      next();
    });
  },
});

export default defineConfig({
  plugins: [react(), adminPages()],
  build: {
    // dist/.gitkeep を残すため。古いファイルは npm run build が消す。
    emptyOutDir: false,
    rollupOptions: { input: { index: "index.html", admin: "admin.html" } },
  },
  server: {
    proxy: { "/api": backend, "/img": backend },
  },
});
