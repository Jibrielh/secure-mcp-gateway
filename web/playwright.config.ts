import { defineConfig } from "@playwright/test";

export default defineConfig({
  testDir: "./e2e",
  timeout: 60_000,
  use: { baseURL: "http://127.0.0.1:18090" },
  webServer: {
    command: "sh ../scripts/e2e-server.sh",
    url: "http://127.0.0.1:18090/healthz",
    reuseExistingServer: false,
    timeout: 120_000,
  },
});
