import { defineConfig } from '@lingui/cli'
import { formatter } from '@lingui/format-po'
// From './src/lib/locales', not './src/lib/locale': this config loads
// under plain Node, and locale.ts statically imports the en catalog
// (a `.po` specifier only Vite's plugin can resolve) for boot
// resilience. locales.ts carries no such import.
import { SUPPORTED_LOCALES } from './src/lib/locales'

export default defineConfig({
  sourceLocale: 'en',
  // 'zz' is the fallback-test fixture locale (src/locales/zz.po,
  // src/lib/catalogFallback.test.tsx). Lingui only compiles a
  // locale's translations if it appears in this list, but gating it
  // here on VITEST - rather than adding it to SUPPORTED_LOCALES -
  // keeps it out of the app's locale registry, the switcher,
  // CATALOG_LOADERS, and `npm run extract`.
  locales: [...SUPPORTED_LOCALES, ...(process.env.VITEST ? ['zz'] : [])],
  // Compile-time fallback: a partially translated catalog fills its
  // gaps with English instead of leaking message ids.
  fallbackLocales: { default: 'en' },
  // No line numbers in `#:` references: a one-line diff in a component
  // would otherwise reshuffle catalog references across every entry
  // below it, drowning real translation changes in refactor churn.
  format: formatter({ lineNumbers: false }),
  catalogs: [
    {
      path: '<rootDir>/src/locales/{locale}',
      include: ['<rootDir>/src'],
    },
  ],
})
