import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { screen } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { jsonResponse } from '../../test/fixtures'
import { renderWithI18n } from '../../test/i18n'
import RematchTrigger from './RematchTrigger'

function renderRematch() {
  const qc = new QueryClient({ defaultOptions: { mutations: { retry: false } } })
  renderWithI18n(
    <QueryClientProvider client={qc}>
      <RematchTrigger />
    </QueryClientProvider>,
  )
}

afterEach(() => vi.unstubAllGlobals())

it('starts an entry rematch', async () => {
  const user = userEvent.setup()
  const fetchMock = vi.fn().mockResolvedValue(jsonResponse(202, { status: 'started' }))
  vi.stubGlobal('fetch', fetchMock)
  renderRematch()
  await user.click(screen.getByRole('button', { name: 'Trigger entry rematch' }))
  expect(await screen.findByText('Rematch started.')).toBeInTheDocument()
  expect(fetchMock.mock.calls[0][0]).toBe('/api/admin/rematch')
})

it('reports an already-running rematch', async () => {
  const user = userEvent.setup()
  vi.stubGlobal('fetch', vi.fn().mockResolvedValue(jsonResponse(409, {
    type: 'about:blank', title: 'Conflict', status: 409, code: 'rematch_in_progress', detail: 'a rematch is already running',
  })))
  renderRematch()
  await user.click(screen.getByRole('button', { name: 'Trigger entry rematch' }))
  expect(await screen.findByRole('alert')).toHaveTextContent('A rematch is already running.')
})
