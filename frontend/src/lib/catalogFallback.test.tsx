import { i18n } from '@lingui/core'
import { cleanup, screen } from '@testing-library/react'
import { MemoryRouter, Route, Routes } from 'react-router'
import { messages as zzMessages } from '../locales/zz.po'
import NotFound from '../pages/NotFound'
import { renderWithI18n } from '../test/i18n'

// Proves a partially translated catalog renders its untranslated
// entries in English, never as raw message ids. src/locales/zz.po is
// a two-entry fixture ('zz' is never a real locale and never joins
// SUPPORTED_LOCALES): msgids copied verbatim from en.po, one with a
// fake translation, one with an empty msgstr. Importing it compiles
// through the same @lingui/vite-plugin -> lingui.config.ts pipeline
// (fallbackLocales included) as every real catalog.
//
// The assertions render NotFound (which owns both fixture strings)
// rather than calling i18n._() with a hand-picked id: compiled
// catalog keys are opaque generated hashes, so a broken fallback
// renders a visible hash that these text queries cannot miss, where
// a hardcoded-id assertion could quietly agree with it.
//
// If fallbackLocales is ever edited: @lingui/cli also falls back to
// sourceLocale unconditionally beneath it, so the untranslated
// assertion stays green even with fallbackLocales emptied; what this
// test fails on is a deeper pipeline break (leaked ids, broken tag
// markup).
afterEach(() => {
  // Unmount before touching the shared singleton: this hook runs
  // ahead of RTL's auto-cleanup (afterEach hooks are LIFO), and
  // re-activating against a still-mounted tree is an I18nProvider
  // update outside act.
  cleanup()
  i18n.activate('en')
})

it('fills an untranslated catalog entry with English instead of leaking its message id', () => {
  i18n.load('zz', zzMessages)
  i18n.activate('zz')
  renderWithI18n(
    <MemoryRouter initialEntries={['/no-such-page']}>
      <Routes>
        <Route path="*" element={<NotFound />} />
      </Routes>
    </MemoryRouter>,
  )

  // Fixture entry with a msgstr: the (fake) translation renders.
  expect(screen.getByRole('heading', { name: 'zz-translated-not-found' })).toBeInTheDocument()

  // Fixture entry with an empty msgstr: the compiled-in English
  // fallback renders verbatim - the full sentence, not a bare hash id
  // and not blank. The link only appears at all if the fallback text
  // still carried valid <0>...</0> tag markup around it.
  const link = screen.getByRole('link', { name: 'Go to the start page' })
  expect(link).toHaveAttribute('href', '/')
  expect(link.closest('p')?.textContent).toBe(
    'There is no page at this address. Go to the start page.',
  )
})
