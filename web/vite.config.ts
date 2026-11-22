import { defineConfig } from "vite";
import react from "@vitejs/plugin-react";

// `make dev` runs the Go server on FH_PORT and opens the UI here. The Go
// server only accepts WebSocket upgrades whose Origin is its own, so the proxy
// rewrites Origin on the way through.
const target = `http://127.0.0.1:${process.env.FH_PORT ?? "4815"}`;

// `headers` is applied to WebSocket upgrades as well as plain requests.
const proxy = { target, ws: true, headers: { origin: target } };

export default defineConfig({
  plugins: [react()],
  server: {
    host: "127.0.0.1",
    port: 5173,
    strictPort: true,
    proxy: { "/auth": proxy, "/api": proxy, "/ws": proxy },
  },
  build: { outDir: "dist", emptyOutDir: true },
});
