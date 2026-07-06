import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { render, screen } from '@testing-library/react'
import { MemoryRouter } from 'react-router'
import { jsonResponse } from '../test/fixtures'
import Recommendations from './Recommendations'

function renderRecs() {
  const qc = new QueryClient({ defaultOptions: { queries: { retry: false } } })
  return render(
    <QueryClientProvider client={qc}>
      <MemoryRouter>
        <Recommendations />
      </MemoryRouter>
    </QueryClientProvider>,
  )
}

afterEach(() => vi.unstubAllGlobals())

const recs = {
  degraded: false,
  recommendations: [
    { igdb_game_id: 1011, name: 'Secret of Mana', genres: ['RPG'], score: 4.5, cover_url: 'https://img.example/som.jpg', first_release_date: '1993-08-06' },
    { igdb_game_id: 1012, name: 'Terranigma', genres: ['RPG', 'Adventure'], score: 3.1 },
  ],
}

it('renders scored cards with add CTAs', async () => {
  vi.stubGlobal('fetch', vi.fn().mockResolvedValue(jsonResponse(200, recs)))
  renderRecs()
  expect(await screen.findByText('Secret of Mana')).toBeInTheDocument()
  expect(screen.getByText('score 4.5')).toBeInTheDocument()
  expect(screen.getByText(/1993/)).toBeInTheDocument()
  const cta = screen.getByRole('link', { name: 'Add Secret of Mana to collection' })
  expect(cta).toHaveAttribute('href', '/add?q=Secret%20of%20Mana')
})

it('flags a degraded scoring pass', async () => {
  vi.stubGlobal('fetch', vi.fn().mockResolvedValue(jsonResponse(200, { ...recs, degraded: true })))
  renderRecs()
  expect(await screen.findByRole('alert')).toHaveTextContent(/some candidates were skipped/i)
})

it('explains an empty answer', async () => {
  vi.stubGlobal('fetch', vi.fn().mockResolvedValue(jsonResponse(200, { degraded: false, recommendations: [] })))
  renderRecs()
  expect(await screen.findByText(/add and rate a few games/i)).toBeInTheDocument()
})
