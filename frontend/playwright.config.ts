import { defineConfig, devices } from '@playwright/test'

// Runs against the live dev stack through the APISIX gateway
// port-forward; nothing is started here. Bring the stack up first
// (task run), then: task e2e
export default defineConfig({
  testDir: './e2e',
  fullyParallel: false,
  workers: 1,
  retries: 0,
  reporter: 'list',
  use: {
    baseURL: process.env.BFF_URL ?? 'http://localhost:8090',
    // Money assertions read navigator.language; pin en-US so symbol
    // placement and grouping stay deterministic across machines.
    locale: 'en-US',
    trace: 'retain-on-failure',
  },
  projects: [{ name: 'chromium', use: { ...devices['Desktop Chrome'] } }],
})
