import { defineConfig } from "vite";
import react from "@vitejs/plugin-react";

const gateway = "http://dash.127.0.0.1.nip.io";

export default defineConfig({
  plugins: [react()],
  server: {
    proxy: {
      "/api": {
        target: gateway,
        changeOrigin: true,
        // The gateway refuses state-changing requests whose Origin host differs from Host.
        headers: { origin: gateway },
      },
    },
  },
});
