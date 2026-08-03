import { i18n, type Messages } from '@lingui/core'
import { messages as enMessages } from '../locales/en.po'
import { recordCatalogFailure, recordLocaleBoot, recordLocaleSwitch } from '../telemetry'
import { LOCALE_NAMES, SUPPORTED_LOCALES } from './locales'
import type { Locale } from './locales'

// The rung of the boot resolution ladder that produced a locale -
// recordLocaleBoot's second argument. Exported so callers besides
// activateBoot (namely main.tsx, via resolveLocaleWithSource) can name
// it.
export type LocaleSource = 'stored' | 'browser' | 'fallback'

// Re-exported so every existing consumer of this module keeps
// resolving these from './lib/locale' unchanged - see locales.ts for
// why the list itself is defined there instead of here.
export { LOCALE_NAMES, SUPPORTED_LOCALES }
export type { Locale }

// Per-locale catalog loaders, colocated with the static en import
// above so en.po keeps exactly one importer in the whole app: this
// one. A locale-specific dynamic import (`import('../locales/xx.po')`)
// is what gives Vite a static specifier to split into that locale's
// own chunk; en instead resolves the catalog already sitting in
// memory above, so activating it never pulls a chunk over the
// network and never gets one of its own in the build. Record<Locale,
// ...> means adding a locale to SUPPORTED_LOCALES without adding its
// loader here fails `tsc -b`, not just at runtime.
export const CATALOG_LOADERS: Record<Locale, () => Promise<{ messages: Messages }>> = {
  en: () => Promise.resolve({ messages: enMessages }),
  ja: () => import('../locales/ja.po'),
}

// Guards use typeof in case this module is ever loaded outside a
// browser.
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

// resolveLocaleWithSource picks the boot locale - stored choice, else
// browser language matched on primary subtag, else en - and names
// which rung of that ladder answered, so activateBoot can record it
// alongside the boot counter. Same ladder as the theme (stored choice,
// environment signal, default).
export function resolveLocaleWithSource(): { locale: Locale; source: LocaleSource } {
  const choice = stored()
  if (choice) return { locale: choice, source: 'stored' }
  const matched = supported(browser()?.split('-')[0])
  if (matched) return { locale: matched, source: 'browser' }
  return { locale: 'en', source: 'fallback' }
}

// resolveLocale is resolveLocaleWithSource's locale alone, kept as its
// own export - unchanged signature - for callers that never needed
// the source.
export function resolveLocale(): Locale {
  return resolveLocaleWithSource().locale
}

// Assistive tech reads pronunciation rules off <html lang>; index.html
// ships lang="en" as the pre-boot default and this keeps it true after
// every activation, including the boot fallback to en.
function applyDocumentLang(locale: Locale): void {
  if (typeof document !== 'undefined') document.documentElement.lang = locale
}

// dynamicActivate loads one locale's catalog through CATALOG_LOADERS
// and switches to it. Only the dynamic-import loaders code-split, so
// a visitor only ever downloads a chunk for a locale other than en.
export async function dynamicActivate(locale: Locale): Promise<void> {
  const { messages } = await CATALOG_LOADERS[locale]()
  i18n.load(locale, messages)
  i18n.activate(locale)
  applyDocumentLang(locale)
}

// activateEn activates the statically bundled English catalog - see
// activateBoot below for why en does not travel through
// dynamicActivate's code-split path like every other locale.
function activateEn(): void {
  i18n.load('en', enMessages)
  i18n.activate('en')
  applyDocumentLang('en')
}

// activateBoot is what main.tsx awaits before its first render. A
// production build runs Lingui's macro, which compiles source text
// out of the app entirely, leaving only message ids behind - the
// active catalog is the only remaining copy of the actual words. If
// that catalog is a chunk that fails to fetch (offline, a stale
// deploy manifest, a flaky network), there is no readable text left
// to render: a blank screen at best, raw ids at worst. en is
// therefore imported statically above instead of code-split, so it
// ships in the same JS the app already needs just to boot, and can
// always be activated without a network round trip. Any other locale
// still code-splits through dynamicActivate; if that chunk fetch
// fails, boot falls back to the statically bundled en catalog so the
// app renders real words instead. Mid-session switches go through
// setLocale below instead, which keeps its own gentler catch: stay
// on the current locale rather than force a switch to English. source
// is resolveLocaleWithSource's verdict for this locale, recorded
// alongside it on success; a chunk failure records the catalog
// failure counter instead of the boot counter - the locale that
// actually ended up active there is the static en fallback, not the
// one this call was asked to activate.
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

// setLocale persists the device-local choice, then activates it. The
// switch is recorded before activation starts, from whatever locale is
// active right now to the requested one - "to" may equal "from" if a
// user reselects their current language.
export async function setLocale(locale: Locale): Promise<void> {
  if (!supported(locale)) return
  recordLocaleSwitch(i18n.locale, locale)
  localStorage.setItem('locale', locale)
  try {
    await dynamicActivate(locale)
  } catch {
    recordCatalogFailure('switch', locale)
    // Catalog chunk unreachable (offline mid-session): the current
    // locale stays active; the persisted choice applies on next load.
  }
}

// formatLocale is the Intl formatting locale, evaluated per call so a
// live switch changes subsequent formatting. A stored choice wins,
// region-refined when the browser agrees on the language (choice de
// in a de-AT browser formats as de-AT); with no stored choice the
// browser language governs, exactly as before this layer existed.
export function formatLocale(): string | undefined {
  const choice = stored()
  const lang = browser()
  if (!choice) return lang
  if (lang && lang.split('-')[0] === choice) return lang
  return choice
}
