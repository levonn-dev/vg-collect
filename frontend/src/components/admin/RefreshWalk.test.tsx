import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { screen } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { jsonResponse } from '../../test/fixtures'
import { renderWithI18n } from '../../test/i18n'
import RefreshWalk from './RefreshWalk'

function renderRefresh() {
  const qc = new QueryClient({ defaultOptions: { mutations: { retry: false } } })
  renderWithI18n(
    <QueryClientProvider client={qc}>
      <RefreshWalk />
    </QueryClientProvider>,
  )
}

afterEach(() => vi.unstubAllGlobals())

it('starts a walk', async () => {
  const user = userEvent.setup()
  const fetchMock = vi.fn().mockResolvedValue(jsonResponse(202, { status: 'started' }))
  vi.stubGlobal('fetch', fetchMock)
  renderRefresh()
  await user.click(screen.getByRole('button', { name: 'Trigger refresh walk' }))
  expect(await screen.findByText('Walk started.')).toBeInTheDocument()
  expect(fetchMock.mock.calls[0][0]).toBe('/api/admin/refresh')
})

it('reports an already-running walk', async () => {
  const user = userEvent.setup()
  vi.stubGlobal('fetch', vi.fn().mockResolvedValue(jsonResponse(409, {
    type: 'about:blank', title: 'Conflict', status: 409, code: 'refresh_in_progress', detail: 'a walk is already running',
  })))
  renderRefresh()
  await user.click(screen.getByRole('button', { name: 'Trigger refresh walk' }))
  expect(await screen.findByRole('alert')).toHaveTextContent('A walk is already running.')
})
