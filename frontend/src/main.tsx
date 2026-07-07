import { StrictMode } from 'react'
import { createRoot } from 'react-dom/client'
import { initTelemetry } from './telemetry'
import App from './App'
import './index.css'

initTelemetry()

createRoot(document.getElementById('root')!).render(
  <StrictMode>
    <App />
  </StrictMode>,
)
