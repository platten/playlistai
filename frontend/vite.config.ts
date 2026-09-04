import { defineConfig } from "vite";
import react from "@vitejs/plugin-react";

// Wails serves the contents of dist/ from an embedded FS, so assets must be
// referenced relatively.
export default defineConfig({
  plugins: [react()],
  base: "./",
  build: {
    outDir: "dist",
    emptyOutDir: true,
  },
});
