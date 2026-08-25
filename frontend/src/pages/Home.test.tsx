import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { screen } from '@testing-library/react'
import { MemoryRouter, Route, Routes } from 'react-router'
import { meFixture } from '../test/fixtures'
import { renderWithI18n } from '../test/i18n'
import Home from './Home'

// staleTime: Infinity keeps the seeded ['me'] cache from refetching
// through the real fetch (same trick as useDisplayMoney.test.tsx).
function renderHome(landingPage: 'collection' | 'feed' | 'explore') {
  const qc = new QueryClient({ defaultOptions: { queries: { retry: false, staleTime: Infinity } } })
  qc.setQueryData(['me'], meFixture({ landing_page: landingPage }))
  return renderWithI18n(
    <QueryClientProvider client={qc}>
      <MemoryRouter initialEntries={['/']}>
        <Routes>
          <Route path="/" element={<Home />} />
          <Route path="/collection" element={<div>collection-page</div>} />
          <Route path="/feed" element={<div>feed-page</div>} />
          <Route path="/explore" element={<div>explore-page</div>} />
        </Routes>
      </MemoryRouter>
    </QueryClientProvider>,
  )
}

it('redirects to /collection when the preference is collection', async () => {
  renderHome('collection')
  expect(await screen.findByText('collection-page')).toBeInTheDocument()
})

it('redirects to /feed when the preference is feed', async () => {
  renderHome('feed')
  expect(await screen.findByText('feed-page')).toBeInTheDocument()
})

it('redirects to /explore when the preference is explore', async () => {
  renderHome('explore')
  expect(await screen.findByText('explore-page')).toBeInTheDocument()
})
