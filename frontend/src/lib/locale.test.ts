import { i18n } from '@lingui/core'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { recordCatalogFailure, recordLocaleBoot, recordLocaleSwitch } from '../telemetry'
import {
  activateBoot,
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

beforeEach(() => {
  localStorage.clear()
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
    // No .po file exists for this locale, so dynamicActivate's import
    // rejects - this exercises the boot fallback branch directly,
    // the same way an unreachable chunk would in production.
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
