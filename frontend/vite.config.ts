/// <reference types="vitest/config" />
import { defineConfig } from 'vite'
import react from '@vitejs/plugin-react'
import babel from '@rolldown/plugin-babel'
import { lingui, linguiTransformerBabelPreset } from '@lingui/vite-plugin'
import tailwindcss from '@tailwindcss/vite'

// The dev server proxies /api to the APISIX gateway port-forward, so
// cookie flows run against the real edge even in dev. The in-cluster
// build is served by the bff itself (same origin, no proxy).
export default defineConfig({
  plugins: [
    react(),
    // @vitejs/plugin-react 6 dropped generic Babel plugin passthrough
    // (its default transform is Rolldown/oxc, not Babel). The Lingui
    // macro plugin is Babel-only, so it is bridged in as its own
    // composed plugin instead of a react() option. enforce: 'pre' (set
    // by @rolldown/plugin-babel itself) runs this ahead of react()'s
    // JSX transform, so it still sees the original <Trans> JSX. Scoped
    // to src so it never touches node_modules; linguiTransformerBabelPreset
    // additionally filters to files whose source references a Lingui
    // macro import, so most src files skip Babel entirely.
    babel({
      include: /[/\\]src[/\\]/,
      exclude: /[/\\]node_modules[/\\]/,
      presets: [linguiTransformerBabelPreset()],
    }),
    lingui(),
    tailwindcss(),
  ],
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
