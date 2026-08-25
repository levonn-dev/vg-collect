import { screen, waitFor } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { describe, expect, it, vi } from 'vitest'
import { renderWithI18n } from '../test/i18n'
import LocaleSwitch from './LocaleSwitch'
import { setLocale, type Locale } from '../lib/locale'

// setLocale persists a device-local choice and swaps the i18n singleton's
// catalog; stubbed by default so tests assert only the argument passed.
vi.mock('../lib/locale', async (importOriginal) => {
  const mod = await importOriginal<typeof import('../lib/locale')>()
  return { ...mod, setLocale: vi.fn().mockResolvedValue(undefined) }
})

type LocaleModule = typeof import('../lib/locale')

// CATALOG_LOADERS is the only seam to simulate an unreachable catalog chunk
// without a network; restores the entry and the storage key even if run throws.
async function withFailingJaCatalog(
  loaders: LocaleModule['CATALOG_LOADERS'],
  run: () => Promise<void>,
): Promise<void> {
  const loader = loaders.ja
  loaders.ja = () => Promise.reject(new Error('chunk unreachable'))
  try {
    await run()
  } finally {
    loaders.ja = loader
    localStorage.removeItem('locale')
  }
}

const ONE: readonly Locale[] = ['en']

describe('LocaleSwitch', () => {
  it('renders nothing while fewer than two locales are supported', () => {
    renderWithI18n(<LocaleSwitch locales={ONE} />)
    expect(screen.queryByRole('combobox')).toBeNull()
  })

  it('lists the supported locales by endonym with the active locale selected', () => {
    renderWithI18n(<LocaleSwitch />)
    const select = screen.getByRole('combobox', { name: 'Language' })
    expect(select).toHaveValue('en')
    expect(screen.getAllByRole('option')).toHaveLength(2)
    expect(screen.getByRole('option', { name: 'English' })).toBeInTheDocument()
    expect(screen.getByRole('option', { name: '日本語' })).toBeInTheDocument()
  })

  it('marks each option with the language its endonym is written in', () => {
    renderWithI18n(<LocaleSwitch />)
    expect(screen.getByRole('option', { name: 'English' })).toHaveAttribute('lang', 'en')
    expect(screen.getByRole('option', { name: '日本語' })).toHaveAttribute('lang', 'ja')
  })

  it('calls setLocale with the picked locale', async () => {
    renderWithI18n(<LocaleSwitch />)
    await userEvent.selectOptions(screen.getByRole('combobox', { name: 'Language' }), 'ja')
    expect(setLocale).toHaveBeenCalledWith('ja')
  })

  it('snaps back to the active locale when the switch fails', async () => {
    // A failed switch must leave the previous locale showing. Runs the
    // module's real setLocale against a catalog chunk that can't load.
    const locale = await vi.importActual<LocaleModule>('../lib/locale')
    vi.mocked(setLocale).mockImplementationOnce(locale.setLocale)
    renderWithI18n(<LocaleSwitch />)
    const select = screen.getByRole('combobox', { name: 'Language' })
    await withFailingJaCatalog(locale.CATALOG_LOADERS, async () => {
      await userEvent.selectOptions(select, 'ja')
      // Persisted choice proves the module's real setLocale ran, not the stub.
      expect(localStorage.getItem('locale')).toBe('ja')
      await waitFor(() => expect(select).toHaveValue('en'))
    })
  })
})
