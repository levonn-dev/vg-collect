import { defineConfig, devices } from '@playwright/test'

// Runs against the live dev stack through the APISIX gateway
// port-forward; nothing is started here. Bring the stack up first
// (task run), then: task e2e
//
// Tests are isolation-first (per-run minted users, API-seeded state),
// so they run fully parallel. The worker count is bounded by the
// gateway's shared per-IP request budget, not by CPU; E2E_WORKERS
// overrides for tuning and for the single-worker isolation check.
export default defineConfig({
  testDir: './e2e',
  // Before any test: sweep accounts and products stranded by earlier
  // runs that died before their teardowns (see global-setup.ts); after
  // a completed run: drop this run's minted manifests (global-teardown).
  globalSetup: './e2e/global-setup.ts',
  globalTeardown: './e2e/global-teardown.ts',
  fullyParallel: true,
  workers: Number(process.env.E2E_WORKERS ?? 4),
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
