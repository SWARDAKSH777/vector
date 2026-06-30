import { defineConfig } from "vite";
import react from "@vitejs/plugin-react";
import tailwindcss from "@tailwindcss/vite";

export default defineConfig({
  plugins: [react(), tailwindcss()],
  server: {
    proxy: {
      // During `npm run dev`, proxy API + redirect calls to the Go backend.
      "/api": "http://127.0.0.1:8081",
    },
  },
  build: {
    outDir: "dist",
  },
});
