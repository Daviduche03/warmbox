import { defineConfig } from "vite";
import react from "@vitejs/plugin-react";
import tailwindcss from "@tailwindcss/vite";
import { fileURLToPath, URL } from "node:url";

// https://vite.dev/config/
export default defineConfig({
  plugins: [react(), tailwindcss()],
  resolve: {
    alias: {
      "@": fileURLToPath(new URL("./src", import.meta.url)),
    },
  },
  server: {
    port: 5173,
    proxy: {
      "/api": "http://localhost:7070",
      "/websockify": {
        target: "ws://localhost:7070",
        ws: true,
      },
      "/vnc": "http://localhost:7070",
      "/d": "http://localhost:7070",
      "/p": "http://localhost:7070",
    },
  },
});
