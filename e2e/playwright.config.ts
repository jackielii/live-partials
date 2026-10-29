import { defineConfig } from "@playwright/test";

export default defineConfig({
  testDir: ".",
  // Every tab shares the lab's in-memory store and stream, so one test at a
  // time keeps each meter reading its own frames.
  workers: 1,
  use: { baseURL: "http://localhost:8899" },
  webServer: {
    command: "npm run e2e:serve",
    port: 8899,
    reuseExistingServer: !process.env.CI,
    timeout: 120_000,
  },
});
