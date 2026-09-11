import { defineConfig } from "vitest/config";

export default defineConfig({
  test: {
    coverage: {
      provider: "v8",
      // Include unimported application modules so missing tests remain visible.
      include: ["src/**/*.{ts,tsx}"],
      exclude: ["src/**/*.d.ts", "src/**/*.{test,spec}.{ts,tsx}"],
      reporter: ["text", "json-summary", "html", "lcov"],
      thresholds: { lines: 95, statements: 95, functions: 95, branches: 95 },
    },
  },
});
