import { i18n, type Messages } from '@lingui/core'
import { messages as enMessages } from '../locales/en.po'
import { recordCatalogFailure, recordLocaleBoot, recordLocaleSwitch } from '../telemetry'
import { LOCALE_NAMES, SUPPORTED_LOCALES } from './locales'
import type { Locale } from './locales'

// Rung of the boot resolution ladder that produced a locale
// (recordLocaleBoot's second arg); exported for main.tsx too.
export type LocaleSource = 'stored' | 'browser' | 'fallback'

// Re-exported so consumers keep resolving these from './lib/locale'
// (locales.ts owns the list itself).
export { LOCALE_NAMES, SUPPORTED_LOCALES }
export type { Locale }

// Dynamic import gives Vite a static specifier to chunk-split other
// locales; en resolves in-memory, no network chunk. Record<Locale, ...>
// makes a missing loader a `tsc -b` failure, not a runtime one.
export const CATALOG_LOADERS: Record<Locale, () => Promise<{ messages: Messages }>> = {
  en: () => Promise.resolve({ messages: enMessages }),
  ja: () => import('../locales/ja.po'),
}

// typeof guards in case this module loads outside a browser.
function supported(value: string | null | undefined): Locale | undefined {
  return SUPPORTED_LOCALES.find((l) => l === value)
}

function stored(): Locale | undefined {
  if (typeof localStorage === 'undefined') return undefined
  return supported(localStorage.getItem('locale'))
}

function browser(): string | undefined {
  return typeof navigator !== 'undefined' ? navigator.language : undefined
}

// Picks stored choice, else browser language (primary subtag), else
// en; names which rung answered for the boot counter.
export function resolveLocaleWithSource(): { locale: Locale; source: LocaleSource } {
  const choice = stored()
  if (choice) return { locale: choice, source: 'stored' }
  const matched = supported(browser()?.split('-')[0])
  if (matched) return { locale: matched, source: 'browser' }
  return { locale: 'en', source: 'fallback' }
}

// resolveLocaleWithSource's locale alone, for callers that never needed the source.
export function resolveLocale(): Locale {
  return resolveLocaleWithSource().locale
}

// Assistive tech reads pronunciation rules off <html lang>; kept true
// after every activation.
function applyDocumentLang(locale: Locale): void {
  if (typeof document !== 'undefined') document.documentElement.lang = locale
}

// Loads a catalog via CATALOG_LOADERS and switches to it; only
// non-en loaders code-split.
export async function dynamicActivate(locale: Locale): Promise<void> {
  const { messages } = await CATALOG_LOADERS[locale]()
  i18n.load(locale, messages)
  i18n.activate(locale)
  applyDocumentLang(locale)
}

// Activates the statically bundled English catalog (see activateBoot
// for why en skips the code-split path).
function activateEn(): void {
  i18n.load('en', enMessages)
  i18n.activate('en')
  applyDocumentLang('en')
}

// Lingui's macro strips source text in production, so a failed chunk
// fetch leaves no readable words. en ships statically so it always
// activates with no network round trip; any other locale falls back
// to it on failure (mid-session failures instead stay put, see setLocale).
export async function activateBoot(locale: Locale, source: LocaleSource): Promise<void> {
  const browserLanguage = browser()
  if (locale === 'en') {
    activateEn()
    recordLocaleBoot(locale, source, browserLanguage)
    return
  }
  try {
    await dynamicActivate(locale)
    recordLocaleBoot(locale, source, browserLanguage)
  } catch {
    recordCatalogFailure('boot', locale)
    activateEn()
  }
}

// Persists the device-local choice, then activates. Switch recorded
// before activation; "to" may equal "from" on a reselect.
export async function setLocale(locale: Locale): Promise<void> {
  if (!supported(locale)) return
  recordLocaleSwitch(i18n.locale, locale)
  localStorage.setItem('locale', locale)
  try {
    await dynamicActivate(locale)
  } catch {
    recordCatalogFailure('switch', locale)
    // Chunk unreachable (offline mid-session): current locale stays
    // active, persisted choice applies next load.
  }
}

// Intl formatting locale, evaluated per call. Stored choice wins,
// region-refined when the browser agrees (de + de-AT browser -> de-AT);
// no stored choice falls to browser language.
export function formatLocale(): string | undefined {
  const choice = stored()
  const lang = browser()
  if (!choice) return lang
  if (lang && lang.split('-')[0] === choice) return lang
  return choice
}
