import { defineConfig } from "@playwright/test";

export default defineConfig({
  testDir: ".",
  // Every tab shares the app's in-memory store and stream, so one test at a
  // time keeps each meter reading its own frames.
  workers: 1,
  projects: [
    { name: "lab", testIgnore: /(datastar|hybrid)\//, use: { baseURL: "http://localhost:8899" } },
    { name: "datastar", testDir: "./datastar", use: { baseURL: "http://localhost:8898" } },
    { name: "hybrid", testDir: "./hybrid", use: { baseURL: "http://localhost:8897" } },
  ],
  webServer: [
    { command: "npm run e2e:serve", port: 8899, reuseExistingServer: !process.env.CI, timeout: 120_000 },
    { command: "npm run e2e:serve:datastar", port: 8898, reuseExistingServer: !process.env.CI, timeout: 120_000 },
    { command: "npm run e2e:serve:hybrid", port: 8897, reuseExistingServer: !process.env.CI, timeout: 120_000 },
  ],
});
