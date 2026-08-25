import js from '@eslint/js'
import globals from 'globals'
import lingui from 'eslint-plugin-lingui'
import reactHooks from 'eslint-plugin-react-hooks'
import reactRefresh from 'eslint-plugin-react-refresh'
import tseslint from 'typescript-eslint'
import { defineConfig, globalIgnores } from 'eslint/config'

export default defineConfig([
  // e2e/ and playwright.config.ts are outside the tsconfig projects;
  // Playwright type-checks them at run time.
  globalIgnores([
    'dist',
    'coverage',
    'playwright-report',
    'test-results',
    'e2e',
    'playwright.config.ts',
  ]),
  {
    files: ['**/*.{ts,tsx}'],
    extends: [
      js.configs.recommended,
      tseslint.configs.recommendedTypeChecked,
      reactHooks.configs.flat.recommended,
      reactRefresh.configs.vite,
    ],
    languageOptions: {
      globals: globals.browser,
      parserOptions: {
        project: ['./tsconfig.app.json', './tsconfig.node.json'],
        tsconfigRootDir: import.meta.dirname,
      },
    },
  },
  // no-unlocalized-strings off: all false positives here (route paths,
  // classNames, API field names).
  lingui.configs['flat/recommended'],
])
