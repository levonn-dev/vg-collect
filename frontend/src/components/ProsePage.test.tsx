import { i18n } from '@lingui/core'
import { screen } from '@testing-library/react'
import { afterEach, describe, expect, it, vi } from 'vitest'
import { renderWithI18n } from '../test/i18n'
import { recordProseFallback } from '../telemetry'
import ProsePage, { type ProseVariants } from './ProsePage'

// Isolates the component from initTelemetry's no-op-before-init state:
// without this, recordProseFallback would be a real but uninitialized
// no-op and the assertions below would have nothing to check.
vi.mock('../telemetry', async (importOriginal) => {
  const mod = await importOriginal<typeof import('../telemetry')>()
  return { ...mod, recordProseFallback: vi.fn() }
})

function En() {
  return <main>english body</main>
}
function Zz() {
  return <main>zz body</main>
}

// Activation is global on the singleton; every test leaves en active.
afterEach(() => {
  i18n.activate('en')
  vi.clearAllMocks()
})

function activateZz() {
  i18n.load('zz', {})
  i18n.activate('zz')
}

describe('ProsePage', () => {
  it('renders the en variant with no notice under en', () => {
    renderWithI18n(<ProsePage variants={{ en: En }} page="about" />)
    expect(screen.getByText('english body')).toBeInTheDocument()
    expect(screen.queryByRole('complementary')).toBeNull()
  })

  it('falls back to en without a notice when the locale has no variant', () => {
    activateZz()
    renderWithI18n(<ProsePage variants={{ en: En }} page="about" />)
    expect(screen.getByText('english body')).toBeInTheDocument()
    expect(screen.queryByRole('complementary')).toBeNull()
  })

  it('renders a translated variant under its notice', () => {
    activateZz()
    const variants = { en: En, zz: Zz } as unknown as ProseVariants
    renderWithI18n(<ProsePage variants={variants} page="about" />)
    expect(screen.getByText('zz body')).toBeInTheDocument()
    expect(screen.getByRole('complementary', { name: 'About this translation' })).toBeInTheDocument()
  })

  it('records the fallback-served counter when en substitutes for a missing variant', () => {
    activateZz()
    renderWithI18n(<ProsePage variants={{ en: En }} page="help" />)
    expect(recordProseFallback).toHaveBeenCalledTimes(1)
    expect(recordProseFallback).toHaveBeenCalledWith('help')
  })

  it('does not record the fallback counter under en', () => {
    renderWithI18n(<ProsePage variants={{ en: En }} page="help" />)
    expect(recordProseFallback).not.toHaveBeenCalled()
  })

  it('does not record the fallback counter when a translated variant is served', () => {
    activateZz()
    const variants = { en: En, zz: Zz } as unknown as ProseVariants
    renderWithI18n(<ProsePage variants={variants} page="help" />)
    expect(recordProseFallback).not.toHaveBeenCalled()
  })
})
