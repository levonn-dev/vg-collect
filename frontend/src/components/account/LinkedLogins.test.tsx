import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { screen, waitFor, within } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import type { Identity } from '../../api/me'
import { requestPath } from '../../test/fixtures'
import { renderWithI18n } from '../../test/i18n'
import LinkedLogins from './LinkedLogins'

const identities: Identity[] = [
  { id: 'i1', provider: 'dev', email: 'alice@example.com', created_at: '2026-01-01T00:00:00Z' },
  { id: 'i2', provider: 'dev', email: 'bob@example.com', created_at: '2026-02-01T00:00:00Z' },
]

function stubFetch(response: Response = new Response(null, { status: 204 })) {
  const fetchMock = vi.fn().mockResolvedValue(response)
  vi.stubGlobal('fetch', fetchMock)
  return fetchMock
}

function renderLinkedLogins(list: Identity[] = identities) {
  const qc = new QueryClient({ defaultOptions: { queries: { retry: false } } })
  return renderWithI18n(
    <QueryClientProvider client={qc}>
      <LinkedLogins identities={list} />
    </QueryClientProvider>,
  )
}

afterEach(() => vi.unstubAllGlobals())

it('lists linked logins with provider and email', async () => {
  renderLinkedLogins()
  const list = await screen.findByRole('list')
  expect(within(list).getByText(/alice@example\.com/)).toBeInTheDocument()
  expect(within(list).getByText(/bob@example\.com/)).toBeInTheDocument()
  expect(within(list).getAllByText('dev')).toHaveLength(2)
})

it('disables Unlink on the last remaining login', async () => {
  renderLinkedLogins([identities[0]])
  const button = await screen.findByRole('button', { name: 'Unlink' })
  expect(button).toBeDisabled()
  expect(button).toHaveAttribute('title', 'Your account needs at least one login')
})

it('unlinks after confirmation', async () => {
  vi.stubGlobal('confirm', vi.fn(() => true))
  const fetchMock = stubFetch()
  renderLinkedLogins()
  const unlinkButtons = await screen.findAllByRole('button', { name: 'Unlink' })
  await userEvent.click(unlinkButtons[1])
  await waitFor(() => {
    expect(fetchMock.mock.calls.some(
      (c) => requestPath(c[0]) === '/api/me/identities/i2' && (c[0] as Request).method === 'DELETE',
    )).toBe(true)
  })
})
