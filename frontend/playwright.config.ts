import { defineConfig, devices } from '@playwright/test'

// Runs against the live dev stack (task run, then task e2e); nothing
// is started here.
//
// Isolation-first (per-run minted users, API-seeded state), so tests
// run fully parallel. Worker count is bounded by the gateway's per-IP
// budget, not CPU; E2E_WORKERS overrides for tuning.
export default defineConfig({
  testDir: './e2e',
  // Sweeps stranded accounts/products before tests (global-setup.ts);
  // drops this run's manifests after (global-teardown).
  globalSetup: './e2e/global-setup.ts',
  globalTeardown: './e2e/global-teardown.ts',
  fullyParallel: true,
  workers: Number(process.env.E2E_WORKERS ?? 4),
  retries: 0,
  reporter: 'list',
  use: {
    baseURL: process.env.BFF_URL ?? 'http://localhost:8090',
    // Money assertions read navigator.language; pin en-US for
    // deterministic symbol placement/grouping.
    locale: 'en-US',
    trace: 'retain-on-failure',
  },
  projects: [{ name: 'chromium', use: { ...devices['Desktop Chrome'] } }],
})
