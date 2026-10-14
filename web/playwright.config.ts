import { defineConfig, devices } from "@playwright/test";

// End-to-end tests on an Android-sized phone against a real server.
// Run with: npm run e2e   (needs Go; the first time: npx playwright install chromium)
export default defineConfig({
  testDir: "e2e",
  fullyParallel: false,
  workers: 1,
  timeout: 60_000,
  reporter: [["list"]],
  use: {
    ...devices["Pixel 7"],
    baseURL: "http://127.0.0.1:8097",
    trace: "retain-on-failure",
  },
  webServer: {
    command: "node e2e/server.mjs",
    url: "http://127.0.0.1:8097/healthz",
    timeout: 180_000,
    reuseExistingServer: false,
  },
});
