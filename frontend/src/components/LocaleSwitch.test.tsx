import { screen } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { describe, expect, it, vi } from 'vitest'
import { renderWithI18n } from '../test/i18n'
import LocaleSwitch from './LocaleSwitch'
import { setLocale, type Locale } from '../lib/locale'

// Two locales exist only in tests until a real second locale ships;
// the module mock supplies the endonym the real record lacks.
vi.mock('../lib/locale', async (importOriginal) => {
  const mod = await importOriginal<typeof import('../lib/locale')>()
  return {
    ...mod,
    LOCALE_NAMES: { en: 'English', de: 'Deutsch' },
    setLocale: vi.fn().mockResolvedValue(undefined),
  }
})

const TWO = ['en', 'de'] as unknown as readonly Locale[]

describe('LocaleSwitch', () => {
  it('renders nothing while fewer than two locales are supported', () => {
    renderWithI18n(<LocaleSwitch />)
    expect(screen.queryByRole('combobox')).toBeNull()
  })

  it('lists locales by endonym with the active locale selected', () => {
    renderWithI18n(<LocaleSwitch locales={TWO} />)
    const select = screen.getByRole('combobox', { name: 'Language' })
    expect(select).toHaveValue('en')
    expect(screen.getByRole('option', { name: 'English' })).toBeInTheDocument()
    expect(screen.getByRole('option', { name: 'Deutsch' })).toBeInTheDocument()
  })

  it('calls setLocale with the picked locale', async () => {
    renderWithI18n(<LocaleSwitch locales={TWO} />)
    await userEvent.selectOptions(screen.getByRole('combobox', { name: 'Language' }), 'de')
    expect(setLocale).toHaveBeenCalledWith('de')
  })
})
