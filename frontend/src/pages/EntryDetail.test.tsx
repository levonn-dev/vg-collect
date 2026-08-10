import { i18n } from '@lingui/core'
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { cleanup, screen, waitFor } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { MemoryRouter, Route, Routes } from 'react-router'
import type { Entry } from '../api/collection'
import { messages as jaMessages } from '../locales/ja.po'
import { entryFixture, jsonResponse } from '../test/fixtures'
import { renderWithI18n } from '../test/i18n'
import EntryDetail from './EntryDetail'

function renderDetail(id: string, qc = new QueryClient({ defaultOptions: { queries: { retry: false } } })) {
  return {
    qc,
    ...renderWithI18n(
      <QueryClientProvider client={qc}>
        <MemoryRouter initialEntries={[`/entries/${id}`]}>
          <Routes>
            <Route path="/entries/:id" element={<EntryDetail />} />
            <Route path="/collection" element={<div>collection-page</div>} />
          </Routes>
        </MemoryRouter>
      </QueryClientProvider>,
    ),
  }
}

afterEach(() => {
  vi.unstubAllGlobals()
  // Order matters: cleanup() before activate() - see EntryTable.test.tsx's
  // afterEach for why (I18nProvider update outside act otherwise).
  cleanup()
  i18n.activate('en')
})

function activateJa() {
  i18n.load('ja', jaMessages)
  i18n.activate('ja')
}

// JP-trio fixture (see EntryTable.test.tsx / productTitle.test.ts);
// cover_url is also set so the localized-cover assertion proves
// precedence, not absence.
const jp: Partial<Entry> = {
  display_name: 'Trials of Mana',
  localized_name: '聖剣伝説 3',
  localized_name_translit: 'Seiken Densetsu 3',
  localized_cover_url: 'https://x/jp.jpg',
  cover_url: 'https://x/na.jpg',
  region: 'ntsc_j',
}

// Every product-backed fixture here also drives ApprovalNotice's own
// submission fetch; answer it with "no submission" so the banner
// stays hidden and each test can assert its own concern undisturbed.
const noSubmission = () =>
  new Response(
    JSON.stringify({ type: 'about:blank', title: 'x', status: 404, code: 'submission_not_found', detail: 'x' }),
    { status: 404, headers: { 'Content-Type': 'application/problem+json' } },
  )

it('renders the catalog header and the form for a product-backed entry', async () => {
  const e = entryFixture({ display_name: 'Chrono Trigger', value_cents: 4200 })
  vi.stubGlobal('fetch', vi.fn().mockImplementation((url: string) =>
    Promise.resolve(String(url).startsWith('/api/tags')
      ? jsonResponse(200, { tags: [] })
      : String(url).endsWith('/submission')
        ? noSubmission()
        : jsonResponse(200, e))))
  renderDetail(e.id)
  expect(await screen.findByRole('heading', { name: 'Chrono Trigger' })).toBeInTheDocument()
  expect(screen.getByText(/SNES/)).toBeInTheDocument()
  expect(screen.getByRole('form', { name: 'Entry editor' })).toBeInTheDocument()
})

it('saves through the form and shows the refreshed entry', async () => {
  const e = entryFixture({ notes: 'old' })
  const updated = { ...e, notes: 'new note' }
  const fetchMock = vi.fn().mockImplementation((url: string, init?: RequestInit) => {
    if (String(url).startsWith('/api/tags')) return Promise.resolve(jsonResponse(200, { tags: [] }))
    if (String(url).endsWith('/submission')) return Promise.resolve(noSubmission())
    if (init?.method === 'PUT') return Promise.resolve(jsonResponse(200, updated))
    return Promise.resolve(jsonResponse(200, e))
  })
  vi.stubGlobal('fetch', fetchMock)
  renderDetail(e.id)
  await userEvent.click(await screen.findByRole('button', { name: /save/i }))
  const put = fetchMock.mock.calls.find((c) => (c[1] as RequestInit | undefined)?.method === 'PUT')
  expect(put?.[0]).toBe(`/api/entries/${e.id}`)
  // Success must be visible, and drifting from the saved state must
  // retract it.
  expect(await screen.findByText('Saved.')).toBeInTheDocument()
  await userEvent.type(screen.getByRole('textbox', { name: 'Notes' }), '!')
  expect(screen.queryByText('Saved.')).not.toBeInTheDocument()
})

it('shows the just-added banner when arriving from the wizard', async () => {
  const e = entryFixture()
  vi.stubGlobal('fetch', vi.fn().mockImplementation((url: string) =>
    Promise.resolve(String(url).startsWith('/api/tags')
      ? jsonResponse(200, { tags: [] })
      : String(url).endsWith('/submission')
        ? noSubmission()
        : jsonResponse(200, e))))
  const qc = new QueryClient({ defaultOptions: { queries: { retry: false } } })
  renderWithI18n(
    <QueryClientProvider client={qc}>
      <MemoryRouter initialEntries={[{ pathname: `/entries/${e.id}`, state: { justAdded: true } }]}>
        <Routes>
          <Route path="/entries/:id" element={<EntryDetail />} />
        </Routes>
      </MemoryRouter>
    </QueryClientProvider>,
  )
  expect(await screen.findByRole('status')).toHaveTextContent(/added to your collection/i)
})

it('invalidates the dashboard and recommendations caches after a save', async () => {
  const e = entryFixture({ notes: 'old' })
  const updated = { ...e, notes: 'new note' }
  vi.stubGlobal('fetch', vi.fn().mockImplementation((url: string, init?: RequestInit) => {
    if (String(url).startsWith('/api/tags')) return Promise.resolve(jsonResponse(200, { tags: [] }))
    if (String(url).endsWith('/submission')) return Promise.resolve(noSubmission())
    if (init?.method === 'PUT') return Promise.resolve(jsonResponse(200, updated))
    return Promise.resolve(jsonResponse(200, e))
  }))
  const qc = new QueryClient({ defaultOptions: { queries: { retry: false } } })
  qc.setQueryData(['dashboard'], { stale: true })
  qc.setQueryData(['recommendations'], { stale: true })
  renderDetail(e.id, qc)
  await userEvent.click(await screen.findByRole('button', { name: /save/i }))
  await waitFor(() => expect(qc.getQueryState(['recommendations'])?.isInvalidated).toBe(true))
  expect(qc.getQueryState(['dashboard'])?.isInvalidated).toBe(true)
})

it('surfaces a 404 pricing problem from the save', async () => {
  const e = entryFixture()
  vi.stubGlobal('fetch', vi.fn().mockImplementation((url: string, init?: RequestInit) => {
    if (String(url).startsWith('/api/tags')) return Promise.resolve(jsonResponse(200, { tags: [] }))
    if (String(url).endsWith('/submission')) return Promise.resolve(noSubmission())
    if (init?.method === 'PUT') {
      return Promise.resolve(jsonResponse(404, {
        type: 'about:blank', title: 'Not Found', status: 404,
        code: 'unknown_pricing_product', detail: 'no such pricing product in the catalog',
      }))
    }
    return Promise.resolve(jsonResponse(200, e))
  }))
  renderDetail(e.id)
  await userEvent.click(await screen.findByRole('button', { name: /save/i }))
  expect(await screen.findByRole('alert')).toHaveTextContent(/no such pricing product/i)
})

it('renders the pricing panel', async () => {
  const e = entryFixture()
  vi.stubGlobal('fetch', vi.fn().mockImplementation((url: string) =>
    Promise.resolve(String(url).startsWith('/api/tags')
      ? jsonResponse(200, { tags: [] })
      : String(url).endsWith('/submission')
        ? noSubmission()
        : jsonResponse(200, e))))
  renderDetail(e.id)
  expect(await screen.findByRole('region', { name: 'Pricing' })).toBeInTheDocument()
})

it('deletes after confirmation and navigates home', async () => {
  const e = entryFixture()
  vi.spyOn(window, 'confirm').mockReturnValue(true)
  vi.stubGlobal('fetch', vi.fn().mockImplementation((url: string, init?: RequestInit) => {
    if (String(url).startsWith('/api/tags')) return Promise.resolve(jsonResponse(200, { tags: [] }))
    if (String(url).endsWith('/submission')) return Promise.resolve(noSubmission())
    if (init?.method === 'DELETE') return Promise.resolve(new Response(null, { status: 204 }))
    return Promise.resolve(jsonResponse(200, e))
  }))
  renderDetail(e.id)
  await userEvent.click(await screen.findByRole('button', { name: /delete/i }))
  expect(await screen.findByText('collection-page')).toBeInTheDocument()
})

it('invalidates dashboard/recommendations and drops the entry cache on delete', async () => {
  const e = entryFixture()
  vi.spyOn(window, 'confirm').mockReturnValue(true)
  // The mock 404s the entry once deleted, like the real server: the
  // removeQueries call races the still-mounted observer's refetch, and
  // a mock that kept serving the entry would let that refetch
  // repopulate the cache and flake the drop assertion below.
  let deleted = false
  vi.stubGlobal('fetch', vi.fn().mockImplementation((url: string, init?: RequestInit) => {
    if (String(url).startsWith('/api/tags')) return Promise.resolve(jsonResponse(200, { tags: [] }))
    if (String(url).endsWith('/submission')) return Promise.resolve(noSubmission())
    if (init?.method === 'DELETE') {
      deleted = true
      return Promise.resolve(new Response(null, { status: 204 }))
    }
    if (deleted && String(url) === `/api/entries/${e.id}`) {
      return Promise.resolve(new Response(
        JSON.stringify({ type: 'about:blank', title: 'x', status: 404, code: 'entry_not_found', detail: 'x' }),
        { status: 404, headers: { 'Content-Type': 'application/problem+json' } },
      ))
    }
    return Promise.resolve(jsonResponse(200, e))
  }))
  const qc = new QueryClient({ defaultOptions: { queries: { retry: false } } })
  qc.setQueryData(['dashboard'], { stale: true })
  qc.setQueryData(['recommendations'], { stale: true })
  renderDetail(e.id, qc)
  await userEvent.click(await screen.findByRole('button', { name: /delete/i }))
  expect(await screen.findByText('collection-page')).toBeInTheDocument()

  expect(qc.getQueryState(['dashboard'])?.isInvalidated).toBe(true)
  expect(qc.getQueryState(['recommendations'])?.isInvalidated).toBe(true)
  // The back button must not be able to render the deleted entry from cache.
  expect(qc.getQueryData(['entry', e.id])).toBeUndefined()
})

it('renders developer and publisher credits from the entry snapshot', async () => {
  const e = entryFixture({
    display_name: 'Metroid Prime',
    developers: ['Retro Studios'], publishers: ['Nintendo'],
  })
  vi.stubGlobal('fetch', vi.fn().mockImplementation((url: string) =>
    Promise.resolve(String(url).startsWith('/api/tags')
      ? jsonResponse(200, { tags: [] })
      : String(url).endsWith('/submission')
        ? noSubmission()
        : jsonResponse(200, e))))
  renderDetail(e.id)
  expect(await screen.findByText('Developed by Retro Studios')).toBeInTheDocument()
  expect(screen.getByText('Published by Nintendo')).toBeInTheDocument()
})

it('joins multiple credited companies into one line', async () => {
  const e = entryFixture({
    display_name: 'Metroid Prime',
    developers: ['Retro Studios', 'Nintendo'], publishers: ['Nintendo'],
  })
  vi.stubGlobal('fetch', vi.fn().mockImplementation((url: string) =>
    Promise.resolve(String(url).startsWith('/api/tags')
      ? jsonResponse(200, { tags: [] })
      : String(url).endsWith('/submission')
        ? noSubmission()
        : jsonResponse(200, e))))
  renderDetail(e.id)
  expect(await screen.findByText('Developed by Retro Studios, Nintendo')).toBeInTheDocument()
  expect(screen.getByText('Published by Nintendo')).toBeInTheDocument()
})

it('omits the credits line when the entry carries no credits', async () => {
  const e = entryFixture({ display_name: 'Metroid Prime' })
  vi.stubGlobal('fetch', vi.fn().mockImplementation((url: string) =>
    Promise.resolve(String(url).startsWith('/api/tags')
      ? jsonResponse(200, { tags: [] })
      : String(url).endsWith('/submission')
        ? noSubmission()
        : jsonResponse(200, e))))
  renderDetail(e.id)
  await screen.findByRole('heading', { name: 'Metroid Prime' })
  expect(screen.queryByText(/Developed by/)).not.toBeInTheDocument()
  expect(screen.queryByText(/Published by/)).not.toBeInTheDocument()
})

it('never fetches a product for a custom entry', async () => {
  const e = entryFixture({ display_name: 'Homebrew Cart', product_id: undefined })
  const fetchMock = vi.fn().mockImplementation((url: string) =>
    Promise.resolve(String(url).startsWith('/api/tags')
      ? jsonResponse(200, { tags: [] })
      : String(url).endsWith('/submission')
        ? noSubmission()
        : jsonResponse(200, e)))
  vi.stubGlobal('fetch', fetchMock)
  renderDetail(e.id)
  await screen.findByRole('heading', { name: 'Homebrew Cart' })
  expect(fetchMock.mock.calls.some((c) => String(c[0]).startsWith('/api/products/'))).toBe(false)
})

it('renders the romanized title, ja-Latn lang, the canonical secondary line, and the localized cover by default', async () => {
  const e = entryFixture(jp)
  vi.stubGlobal('fetch', vi.fn().mockImplementation((url: string) =>
    Promise.resolve(String(url).startsWith('/api/tags')
      ? jsonResponse(200, { tags: [] })
      : String(url).endsWith('/submission')
        ? noSubmission()
        : jsonResponse(200, e))))
  renderDetail(e.id)
  // The lang attribute rides the inner text span, not the <h2> itself
  // (the heading query below only proves the accessible name/role).
  expect(await screen.findByRole('heading', { name: 'Seiken Densetsu 3' })).toBeInTheDocument()
  expect(screen.getByText('Seiken Densetsu 3')).toHaveAttribute('lang', 'ja-Latn')
  expect(screen.getByText('Trials of Mana')).toBeInTheDocument()
  expect(document.querySelector('img')).toHaveAttribute('src', 'https://x/jp.jpg')
})

it('renders the native title and ja lang under the ja locale', async () => {
  const e = entryFixture(jp)
  vi.stubGlobal('fetch', vi.fn().mockImplementation((url: string) =>
    Promise.resolve(String(url).startsWith('/api/tags')
      ? jsonResponse(200, { tags: [] })
      : String(url).endsWith('/submission')
        ? noSubmission()
        : jsonResponse(200, e))))
  activateJa()
  renderDetail(e.id)
  expect(await screen.findByRole('heading', { name: '聖剣伝説 3' })).toBeInTheDocument()
  expect(screen.getByText('聖剣伝説 3')).toHaveAttribute('lang', 'ja')
})

// Name-only korea shape: the ko-KR bundle carries no transliteration,
// so under the en locale the canonical title leads and the native
// title moves to the secondary line with its own lang tag.
const kr: Partial<Entry> = {
  display_name: 'Trials of Mana',
  localized_name: '성검전설 3',
  region: 'korea',
}

it('renders the canonical title with the native secondary for a name-only korea entry', async () => {
  const e = entryFixture(kr)
  vi.stubGlobal('fetch', vi.fn().mockImplementation((url: string) =>
    Promise.resolve(String(url).startsWith('/api/tags')
      ? jsonResponse(200, { tags: [] })
      : String(url).endsWith('/submission')
        ? noSubmission()
        : jsonResponse(200, e))))
  renderDetail(e.id)
  expect(await screen.findByRole('heading', { name: 'Trials of Mana' })).toBeInTheDocument()
  expect(screen.getByText('Trials of Mana')).not.toHaveAttribute('lang')
  expect(screen.getByText('성검전설 3')).toHaveAttribute('lang', 'ko')
})

it('renders the native korea title under the ja locale', async () => {
  const e = entryFixture(kr)
  vi.stubGlobal('fetch', vi.fn().mockImplementation((url: string) =>
    Promise.resolve(String(url).startsWith('/api/tags')
      ? jsonResponse(200, { tags: [] })
      : String(url).endsWith('/submission')
        ? noSubmission()
        : jsonResponse(200, e))))
  activateJa()
  renderDetail(e.id)
  expect(await screen.findByRole('heading', { name: '성검전설 3' })).toBeInTheDocument()
  expect(screen.getByText('성검전설 3')).toHaveAttribute('lang', 'ko')
})

it('omits the secondary line and the lang attribute for a canonical-only entry', async () => {
  const e = entryFixture({ display_name: 'Chrono Trigger' })
  vi.stubGlobal('fetch', vi.fn().mockImplementation((url: string) =>
    Promise.resolve(String(url).startsWith('/api/tags')
      ? jsonResponse(200, { tags: [] })
      : String(url).endsWith('/submission')
        ? noSubmission()
        : jsonResponse(200, e))))
  renderDetail(e.id)
  await screen.findByRole('heading', { name: 'Chrono Trigger' })
  expect(screen.getByText('Chrono Trigger')).not.toHaveAttribute('lang')
  // Only the heading shows the name; no separate secondary line repeats it.
  expect(screen.getAllByText('Chrono Trigger')).toHaveLength(1)
})
