/// <reference types="vitest/config" />
import { defineConfig } from 'vite'
import react from '@vitejs/plugin-react'
import babel from '@rolldown/plugin-babel'
import { lingui, linguiTransformerBabelPreset } from '@lingui/vite-plugin'
import tailwindcss from '@tailwindcss/vite'

// Dev server proxies /api to the gateway port-forward, so cookie flows
// run against the real edge; in-cluster build is served same-origin, no proxy.
export default defineConfig({
  plugins: [
    react(),
    // plugin-react 6 dropped Babel passthrough (default transform is
    // oxc); Lingui's macro is Babel-only, so it's bridged as its own
    // plugin. enforce: 'pre' runs it before react()'s JSX transform, so
    // it still sees <Trans> JSX. Scoped to src; the preset further skips non-macro files.
    babel({
      include: /[/\\]src[/\\]/,
      exclude: /[/\\]node_modules[/\\]/,
      presets: [linguiTransformerBabelPreset()],
    }),
    lingui(),
    tailwindcss(),
  ],
  build: {
    // Entry chunk sits at ~750kB minified (217kB gzip) after the
    // telemetry-SDK and admin-route splits; threshold set just above so
    // it trips only if the entry grows meaningfully.
    chunkSizeWarningLimit: 800,
  },
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
      // Entrypoint wiring and generated code are validated by e2e
      // smoke and the codegen drift check, not unit tests.
      exclude: ['src/main.tsx', 'src/api/schema.ts', 'src/gen/**', '**/*.d.ts', 'src/test/**', '**/*.css'],
      thresholds: { lines: 80, branches: 80, functions: 80, statements: 80 },
    },
  },
})
