import { i18n } from '@lingui/core'
import { cleanup, screen } from '@testing-library/react'
import { MemoryRouter, Route, Routes } from 'react-router'
import { messages as zzMessages } from '../locales/zz.po'
import NotFound from '../pages/NotFound'
import { renderWithI18n } from '../test/i18n'

// Proves partial translation renders English, never raw message ids.
// zz.po is a 2-entry fixture (zz never joins SUPPORTED_LOCALES): one
// translated entry, one empty msgstr; compiles through the real lingui pipeline.
//
// Asserts via NotFound's rendered text, not i18n._() with a hand-picked
// id: compiled keys are opaque hashes, so a broken fallback shows a
// visible hash these queries catch but a hardcoded-id assertion could miss.
//
// If fallbackLocales is ever removed, @lingui/cli still falls back to
// sourceLocale, so this test stays green regardless; it only catches a
// deeper pipeline break (leaked ids, broken tag markup).
afterEach(() => {
  // Unmount before touching the singleton: afterEach hooks are LIFO,
  // and re-activating a still-mounted tree is an update outside act.
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

  // Empty msgstr: English fallback renders verbatim, not a hash or
  // blank; the link only appears if <0>...</0> tag markup survived.
  const link = screen.getByRole('link', { name: 'Go to the start page' })
  expect(link).toHaveAttribute('href', '/')
  expect(link.closest('p')?.textContent).toBe(
    'There is no page at this address. Go to the start page.',
  )
})
