import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { screen } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { jsonResponse } from '../../test/fixtures'
import { renderWithI18n } from '../../test/i18n'
import ResnapshotTrigger from './ResnapshotTrigger'

function renderResnapshot() {
  const qc = new QueryClient({ defaultOptions: { mutations: { retry: false } } })
  renderWithI18n(
    <QueryClientProvider client={qc}>
      <ResnapshotTrigger />
    </QueryClientProvider>,
  )
}

afterEach(() => vi.unstubAllGlobals())

it('runs the sweep and renders counts', async () => {
  const user = userEvent.setup()
  const fetchMock = vi.fn().mockResolvedValue(
    jsonResponse(200, { products_seen: 3, products_failed: 1, entries_updated: 2 }),
  )
  vi.stubGlobal('fetch', fetchMock)
  renderResnapshot()
  await user.click(screen.getByRole('button', { name: 'Run entry resnapshot' }))
  expect(await screen.findByText('Products seen 3, failed 1, entries updated 2.')).toBeInTheDocument()
  expect(fetchMock.mock.calls[0][0]).toBe('/api/admin/resnapshot')
})

it('failure renders the alert', async () => {
  const user = userEvent.setup()
  vi.stubGlobal('fetch', vi.fn().mockResolvedValue(jsonResponse(502, {
    type: 'about:blank', title: 'Bad Gateway', status: 502, code: 'upstream_error',
  })))
  renderResnapshot()
  await user.click(screen.getByRole('button', { name: 'Run entry resnapshot' }))
  expect(await screen.findByRole('alert')).toHaveTextContent('The sweep failed - try again.')
})
