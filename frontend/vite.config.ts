/// <reference types="vitest/config" />
import { defineConfig } from 'vite'
import react from '@vitejs/plugin-react'
import tailwindcss from '@tailwindcss/vite'

// The dev server proxies /api to the APISIX gateway port-forward, so
// cookie flows run against the real edge even in dev. The in-cluster
// build is served by the bff itself (same origin, no proxy).
export default defineConfig({
  plugins: [react(), tailwindcss()],
  server: {
    proxy: { '/api': 'http://localhost:8090' },
  },
  test: {
    environment: 'jsdom',
    globals: true,
    setupFiles: ['src/test/setup.ts'],
    include: ['src/**/*.test.{ts,tsx}'],
    coverage: {
      provider: 'v8',
      include: ['src/**'],
      // Entrypoint wiring and generated types are validated by the
      // e2e smoke and the codegen drift check, not unit tests.
      exclude: ['src/main.tsx', 'src/api/schema.d.ts', '**/*.d.ts', 'src/test/**', '**/*.css'],
      thresholds: { lines: 80, branches: 80, functions: 80, statements: 80 },
    },
  },
})
