import { StrictMode } from 'react'
import { createRoot } from 'react-dom/client'
import { i18n } from '@lingui/core'
import { I18nProvider } from '@lingui/react'
import { initTelemetry } from './telemetry'
import { activateBoot, resolveLocaleWithSource } from './lib/locale'
import ErrorBoundary from './components/ErrorBoundary'
import App from './App'
import './index.css'

// Unawaited: first render must never wait on the telemetry chunk.
// Records before it lands buffer in the facade and replay after init.
void initTelemetry()

// Catalog must be active before first render (no flash of untranslated
// content); activateBoot falls back to en if a chunk fails, so this always resolves.
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
