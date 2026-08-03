import { i18n } from '@lingui/core'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { recordCatalogFailure, recordLocaleBoot, recordLocaleSwitch } from '../telemetry'
import {
  activateBoot,
  CATALOG_LOADERS,
  dynamicActivate,
  formatLocale,
  resolveLocale,
  resolveLocaleWithSource,
  setLocale,
} from './locale'

// Isolates activateBoot/setLocale's telemetry calls from
// initTelemetry's no-op-before-init state, the same way
// ProsePage.test.tsx and LocaleSwitch.test.tsx mock their own
// sibling-module dependency - what's under test here is which
// function gets called with which arguments, not the counters
// themselves (telemetry.test.ts covers those directly).
vi.mock('../telemetry', async (importOriginal) => {
  const mod = await importOriginal<typeof import('../telemetry')>()
  return {
    ...mod,
    recordCatalogFailure: vi.fn(),
    recordLocaleBoot: vi.fn(),
    recordLocaleSwitch: vi.fn(),
  }
})

function stubLanguage(tag: string) {
  vi.spyOn(window.navigator, 'language', 'get').mockReturnValue(tag)
}

// A rejected loader is what an unreachable catalog chunk looks like to
// this module, and CATALOG_LOADERS is the only seam that can produce
// one without a network. The entry is restored even when run throws.
async function withFailingJaCatalog(run: () => Promise<void>): Promise<void> {
  const loader = CATALOG_LOADERS.ja
  CATALOG_LOADERS.ja = () => Promise.reject(new Error('chunk unreachable'))
  try {
    await run()
  } finally {
    CATALOG_LOADERS.ja = loader
  }
}

beforeEach(() => {
  localStorage.clear()
  // Activation writes <html lang>; start from jsdom's unset default.
  document.documentElement.lang = ''
})

afterEach(() => {
  vi.restoreAllMocks()
  vi.mocked(recordCatalogFailure).mockClear()
  vi.mocked(recordLocaleBoot).mockClear()
  vi.mocked(recordLocaleSwitch).mockClear()
  // Tests share the module-level singleton; leave en active for other files.
  i18n.activate('en')
})

describe('resolveLocale', () => {
  it('uses a stored supported choice', () => {
    localStorage.setItem('locale', 'en')
    stubLanguage('fr-FR')
    expect(resolveLocale()).toBe('en')
  })

  it('ignores a stored unsupported value', () => {
    localStorage.setItem('locale', 'xx')
    stubLanguage('en-GB')
    expect(resolveLocale()).toBe('en')
  })

  it('matches the browser language on primary subtag', () => {
    stubLanguage('en-GB')
    expect(resolveLocale()).toBe('en')
  })

  it('falls back to en when nothing matches', () => {
    stubLanguage('fr-FR')
    expect(resolveLocale()).toBe('en')
  })
})

describe('resolveLocaleWithSource', () => {
  it('names the source as stored for a stored supported choice', () => {
    localStorage.setItem('locale', 'en')
    stubLanguage('fr-FR')
    expect(resolveLocaleWithSource()).toEqual({ locale: 'en', source: 'stored' })
  })

  it('names the source as browser when no choice is stored but the browser language matches', () => {
    stubLanguage('en-GB')
    expect(resolveLocaleWithSource()).toEqual({ locale: 'en', source: 'browser' })
  })

  it('names the source as fallback when nothing matches', () => {
    stubLanguage('fr-FR')
    expect(resolveLocaleWithSource()).toEqual({ locale: 'en', source: 'fallback' })
  })
})

describe('setLocale', () => {
  it('persists and activates a supported locale', async () => {
    await setLocale('en')
    expect(localStorage.getItem('locale')).toBe('en')
    expect(i18n.locale).toBe('en')
  })

  it('rejects an unsupported value without persisting', async () => {
    await setLocale('xx' as never)
    expect(localStorage.getItem('locale')).toBeNull()
  })

  it('records the switch from the currently active locale before activating', async () => {
    i18n.activate('en')
    await setLocale('en')
    expect(recordLocaleSwitch).toHaveBeenCalledTimes(1)
    expect(recordLocaleSwitch).toHaveBeenCalledWith('en', 'en')
  })

  it('does not record a switch for a rejected unsupported value', async () => {
    await setLocale('xx' as never)
    expect(recordLocaleSwitch).not.toHaveBeenCalled()
  })

  it('activates a code-split locale and marks it on the document', async () => {
    await setLocale('ja')
    expect(i18n.locale).toBe('ja')
    expect(document.documentElement.lang).toBe('ja')
  })

  it('keeps the current locale active and marked when the chunk fails', async () => {
    await setLocale('en')
    await withFailingJaCatalog(() => setLocale('ja'))
    expect(i18n.locale).toBe('en')
    // <html lang> names the language actually on screen. A failed
    // switch leaves English text, so it must still name English.
    expect(document.documentElement.lang).toBe('en')
    expect(recordCatalogFailure).toHaveBeenCalledTimes(1)
    expect(recordCatalogFailure).toHaveBeenCalledWith('switch', 'ja')
    // The choice outlives the failed activation and applies next load.
    expect(localStorage.getItem('locale')).toBe('ja')
  })
})

describe('dynamicActivate', () => {
  it('loads the catalog chunk and activates it', async () => {
    await dynamicActivate('en')
    expect(i18n.locale).toBe('en')
  })
})

describe('activateBoot', () => {
  it('activates en synchronously from the static catalog', async () => {
    await activateBoot('en', 'stored')
    expect(i18n.locale).toBe('en')
  })

  it('falls back to the static en catalog when a locale chunk fails to load', async () => {
    // An unregistered locale has no CATALOG_LOADERS entry, so
    // dynamicActivate rejects the way an unreachable chunk does.
    await activateBoot('xx' as never, 'stored')
    expect(i18n.locale).toBe('en')
  })

  it('records the boot counter on success, with the source and the raw browser language', async () => {
    stubLanguage('en-GB')
    await activateBoot('en', 'browser')
    expect(recordLocaleBoot).toHaveBeenCalledTimes(1)
    expect(recordLocaleBoot).toHaveBeenCalledWith('en', 'browser', 'en-GB')
    expect(recordCatalogFailure).not.toHaveBeenCalled()
  })

  it('records a catalog failure instead of a boot when a locale chunk fails to load', async () => {
    await activateBoot('xx' as never, 'stored')
    expect(recordCatalogFailure).toHaveBeenCalledTimes(1)
    expect(recordCatalogFailure).toHaveBeenCalledWith('boot', 'xx')
    expect(recordLocaleBoot).not.toHaveBeenCalled()
  })

  it('activates a code-split locale, records the boot counter, and marks the document', async () => {
    stubLanguage('ja-JP')
    await activateBoot('ja', 'stored')
    expect(i18n.locale).toBe('ja')
    expect(recordLocaleBoot).toHaveBeenCalledTimes(1)
    expect(recordLocaleBoot).toHaveBeenCalledWith('ja', 'stored', 'ja-JP')
    expect(document.documentElement.lang).toBe('ja')
  })

  it('marks the document en when the boot catalog fails and en takes over', async () => {
    await withFailingJaCatalog(() => activateBoot('ja', 'stored'))
    expect(i18n.locale).toBe('en')
    expect(recordCatalogFailure).toHaveBeenCalledTimes(1)
    expect(recordCatalogFailure).toHaveBeenCalledWith('boot', 'ja')
    expect(recordLocaleBoot).not.toHaveBeenCalled()
    expect(document.documentElement.lang).toBe('en')
  })
})

describe('formatLocale', () => {
  it('returns the browser language when no choice is stored', () => {
    stubLanguage('fr-FR')
    expect(formatLocale()).toBe('fr-FR')
  })

  it('region-refines a choice the browser agrees with', () => {
    localStorage.setItem('locale', 'en')
    stubLanguage('en-GB')
    expect(formatLocale()).toBe('en-GB')
  })

  it('returns the bare choice when the browser language differs', () => {
    localStorage.setItem('locale', 'en')
    stubLanguage('fr-FR')
    expect(formatLocale()).toBe('en')
  })
})
