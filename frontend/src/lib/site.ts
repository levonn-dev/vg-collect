// site() resolves this deployment's identity from VITE_SITE_*
// variables baked in at build time: what the instance is called, who
// runs it, and which providers are active. Reads happen per call so
// tests can stub the env.
import { msg } from '@lingui/core/macro'
import type { MessageDescriptor } from '@lingui/core'

export type DataSource = {
  key: string
  label: string
  dataType: MessageDescriptor
  url: string
}

export type AuthProvider = {
  key: string
  label: string
}

export type Site = {
  name: string
  operator: string
  contact: string
  jurisdiction: string
  sourceUrl: string
  dataSources: DataSource[]
  authProviders: AuthProvider[]
}

// The catalog is code knowledge: every source the app can credit,
// with its wording. Which entries are active is deployment config
// (the CSVs). dev login is absent on purpose: legal text must never
// name it.
const DATA_SOURCES: DataSource[] = [
  { key: 'igdb', label: 'IGDB', dataType: msg`Game data`, url: 'https://www.igdb.com' },
  {
    key: 'pricecharting',
    label: 'PriceCharting',
    dataType: msg`Price data`,
    url: 'https://www.pricecharting.com',
  },
  {
    key: 'frankfurter',
    label: 'frankfurter.dev (ECB data)',
    dataType: msg`Exchange rates`,
    url: 'https://frankfurter.dev',
  },
]

const AUTH_PROVIDERS: AuthProvider[] = [
  { key: 'google', label: 'Google' },
  { key: 'twitch', label: 'Twitch' },
]

// Unset and empty both mean none (what is set is what exists), and
// unknown keys are dropped. Result order is catalog order.
function fromCsv<T extends { key: string }>(catalog: T[], csv: string | undefined): T[] {
  const keys = (csv ?? '')
    .split(',')
    .map((k) => k.trim())
    .filter((k) => k !== '')
  return catalog.filter((c) => keys.includes(c.key))
}

export function site(): Site {
  const env = import.meta.env
  return {
    name: env.VITE_SITE_NAME || 'vgkeep',
    operator: env.VITE_SITE_OPERATOR || '',
    contact: env.VITE_SITE_CONTACT || '',
    jurisdiction: env.VITE_SITE_JURISDICTION || '',
    sourceUrl: env.VITE_SITE_SOURCE_URL || 'https://github.com/levonn-dev/vgkeep',
    dataSources: fromCsv(DATA_SOURCES, env.VITE_SITE_DATA_SOURCES),
    authProviders: fromCsv(AUTH_PROVIDERS, env.VITE_SITE_AUTH_PROVIDERS),
  }
}
