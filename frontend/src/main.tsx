import { StrictMode } from 'react'
import { createRoot } from 'react-dom/client'
import { i18n } from '@lingui/core'
import { I18nProvider } from '@lingui/react'
import { initTelemetry } from './telemetry'
import { activateBoot, resolveLocaleWithSource } from './lib/locale'
import ErrorBoundary from './components/ErrorBoundary'
import App from './App'
import './index.css'

initTelemetry()

// The catalog must be active before first render: no flash of
// untranslated content, and no render before a catalog is loaded.
// activateBoot falls back to the statically bundled en catalog if a
// non-en chunk fails to fetch, so this always resolves. The source
// travels alongside the locale so activateBoot can record which rung
// of the resolution ladder picked it.
const { locale, source } = resolveLocaleWithSource()
await activateBoot(locale, source)

createRoot(document.getElementById('root')!).render(
  <StrictMode>
    <I18nProvider i18n={i18n}>
      <ErrorBoundary>
        <App />
      </ErrorBoundary>
    </I18nProvider>
  </StrictMode>,
)
