/// <reference types="vite/client" />

interface ImportMetaEnv {
  readonly VITE_SITE_NAME?: string
  readonly VITE_SITE_OPERATOR?: string
  readonly VITE_SITE_CONTACT?: string
  readonly VITE_SITE_JURISDICTION?: string
  readonly VITE_SITE_SOURCE_URL?: string
  readonly VITE_SITE_DATA_SOURCES?: string
  readonly VITE_SITE_AUTH_PROVIDERS?: string
  // Stamped onto browser telemetry as service.version when set; see
  // telemetry.ts. Unset means no version attribute is emitted.
  readonly VITE_BUILD_VERSION?: string
}

declare module '*.po' {
  import type { Messages } from '@lingui/core'
  export const messages: Messages
}
