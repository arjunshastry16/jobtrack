import react from "@vitejs/plugin-react";
import { defineConfig } from "vite";

// In development the Go API runs on its own (`make run`, port 8090) and Vite proxies
// API calls to it, so the browser sees a single origin, as it will in production when
// the Go binary serves the built app itself.
const api = "http://127.0.0.1:8090";

export default defineConfig({
  plugins: [react()],
  server: {
    host: "127.0.0.1", // loopback only, like everything else on the Pi
    port: 5173,
    strictPort: true,
    proxy: { "/api": api, "/healthz": api, "/readyz": api },
  },
  build: {
    sourcemap: true,
  },
});
