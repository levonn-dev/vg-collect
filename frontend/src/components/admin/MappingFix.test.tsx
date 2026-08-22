import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { cleanup, screen } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import type { Product } from '../../api/catalog'
import { calledPath, jsonResponse, problemResponse, putBody, requestPath } from '../../test/fixtures'
import { renderWithI18n } from '../../test/i18n'
import MappingFix from './MappingFix'

function renderFix(product: Product, onDone = vi.fn()) {
  const qc = new QueryClient({ defaultOptions: { queries: { retry: false }, mutations: { retry: false } } })
  renderWithI18n(
    <QueryClientProvider client={qc}>
      <MappingFix product={product} onDone={onDone} />
    </QueryClientProvider>,
  )
  return onDone
}

afterEach(() => {
  vi.unstubAllGlobals()
  vi.restoreAllMocks()
})

const unmatched: Product = {
  id: 'p1', type: 'game', name: 'Super Mario 64',
  platform: { igdb_platform_id: 4, name: 'Nintendo 64' },
  created_at: '2026-07-01T00:00:00Z', updated_at: '2026-07-01T00:00:00Z',
}

const matched: Product = {
  ...unmatched,
  id: 'p2',
  pricecharting: {
    pc_product_id: 5005, pc_name: 'Super Mario 64', console_name: 'Nintendo 64',
    match_confidence: 1, verified: true, as_of: '2026-07-01T00:00:00Z',
  },
}

it('states the unmatched status and the held badge', () => {
  vi.stubGlobal('fetch', vi.fn())
  renderFix({ ...unmatched, match_hold: true })
  expect(screen.getByText(/unmatched/i)).toBeInTheDocument()
  expect(screen.getByText('held')).toBeInTheDocument()
})

it('picks a listing and PUTs the mapping', async () => {
  const user = userEvent.setup()
  const fetchMock = vi.fn().mockImplementation((url: unknown) => {
    if (requestPath(url).startsWith('/api/search')) {
      return Promise.resolve(jsonResponse(200, {
        degraded: false,
        results: [{ type: 'pc_listing', name: 'Super Mario 64', pc_product_id: 5005, console_name: 'Nintendo 64', loose_cents: 4000, cib_cents: 9000, new_cents: 30000 }],
      }))
    }
    return Promise.resolve(jsonResponse(200, matched))
  })
  vi.stubGlobal('fetch', fetchMock)
  const onDone = renderFix(unmatched)

  await user.click(screen.getByRole('button', { name: 'Choose listing' }))
  const dialog = await screen.findByRole('dialog', { name: 'Match a price listing' })
  expect(dialog).toBeInTheDocument()
  await user.click(screen.getByRole('button', { name: 'Search' }))
  await user.click(await screen.findByRole('button', { name: /Use Super Mario 64/ }))

  const put = fetchMock.mock.calls.find(([input]) => (input as Request).method === 'PUT')
  expect(requestPath(put?.[0])).toBe('/api/admin/products/p1/pricecharting')
  expect(await putBody(put?.[0])).toEqual({ pc_product_id: 5005 })
  expect(onDone).toHaveBeenCalled()
})

it('clears the mapping behind a confirmation', async () => {
  const user = userEvent.setup()
  vi.spyOn(window, 'confirm').mockReturnValue(true)
  const fetchMock = vi.fn().mockResolvedValue(jsonResponse(200, { ...unmatched, id: 'p2', match_hold: true }))
  vi.stubGlobal('fetch', fetchMock)
  const onDone = renderFix(matched)

  await user.click(screen.getByRole('button', { name: 'Clear mapping' }))
  expect(window.confirm).toHaveBeenCalled()
  expect(calledPath(fetchMock, 0)).toBe('/api/admin/products/p2/pricecharting')
  expect(await putBody(fetchMock.mock.calls[0][0])).toEqual({ pc_product_id: null })
  expect(onDone).toHaveBeenCalled()
})

it('does not clear when the confirmation is declined', async () => {
  const user = userEvent.setup()
  vi.spyOn(window, 'confirm').mockReturnValue(false)
  const fetchMock = vi.fn()
  vi.stubGlobal('fetch', fetchMock)
  renderFix(matched)
  await user.click(screen.getByRole('button', { name: 'Clear mapping' }))
  expect(fetchMock).not.toHaveBeenCalled()
})

it('renders the identity_taken conflict with the server-named holder', async () => {
  const user = userEvent.setup()
  vi.spyOn(window, 'confirm').mockReturnValue(true)
  vi.stubGlobal('fetch', vi.fn().mockResolvedValue(problemResponse(
    409,
    'identity_taken',
    'another product with the same identity already carries that listing (holder: 8563fd43 "Tony Hawk\'s Pro Skater")',
  )))
  renderFix(matched)
  await user.click(screen.getByRole('button', { name: 'Clear mapping' }))
  const alert = await screen.findByRole('alert')
  expect(alert).toHaveTextContent(/already carries that listing/i)
  expect(alert).toHaveTextContent(/holder: 8563fd43/i)
})

// Regression for the resolveApiError refactor: identity_taken prefers
// e.message over its own fixed text, but only when the server actually
// sent one - an empty detail must still fall through to the fixed
// text, not render a blank alert.
it('falls back to the fixed identity_taken text when the server sends no detail', async () => {
  const user = userEvent.setup()
  vi.spyOn(window, 'confirm').mockReturnValue(true)
  vi.stubGlobal('fetch', vi.fn().mockResolvedValue(problemResponse(409, 'identity_taken', '')))
  renderFix(matched)
  await user.click(screen.getByRole('button', { name: 'Clear mapping' }))
  const alert = await screen.findByRole('alert')
  expect(alert).toHaveTextContent('Another product already carries that identity - the mapping was not changed.')
})

it('offers Hold on an unmatched, unheld product and parks it behind a confirmation', async () => {
  const user = userEvent.setup()
  vi.spyOn(window, 'confirm').mockReturnValue(true)
  const fetchMock = vi.fn().mockResolvedValue(jsonResponse(200, { ...unmatched, match_hold: true }))
  vi.stubGlobal('fetch', fetchMock)
  const onDone = renderFix(unmatched)

  await user.click(screen.getByRole('button', { name: 'Hold' }))
  expect(window.confirm).toHaveBeenCalled()
  expect(calledPath(fetchMock, 0)).toBe('/api/admin/products/p1/pricecharting')
  expect(await (fetchMock.mock.calls[0][0] as Request).clone().text()).toBe(JSON.stringify({ pc_product_id: null }))
  expect(onDone).toHaveBeenCalled()
})

it('hides Hold on held and on matched products', () => {
  vi.stubGlobal('fetch', vi.fn())
  renderFix({ ...unmatched, match_hold: true })
  expect(screen.queryByRole('button', { name: 'Hold' })).not.toBeInTheDocument()
  cleanup()
  renderFix(matched)
  expect(screen.queryByRole('button', { name: 'Hold' })).not.toBeInTheDocument()
})

it('deletes an unmatched product behind a confirmation', async () => {
  const user = userEvent.setup()
  vi.spyOn(window, 'confirm').mockReturnValue(true)
  const fetchMock = vi.fn().mockResolvedValue(new Response(null, { status: 204 }))
  vi.stubGlobal('fetch', fetchMock)
  const onDone = renderFix({ ...unmatched, match_hold: true })

  await user.click(screen.getByRole('button', { name: 'Delete' }))
  expect(window.confirm).toHaveBeenCalled()
  expect(calledPath(fetchMock, 0)).toBe('/api/admin/products/p1')
  expect((fetchMock.mock.calls[0][0] as Request).method).toBe('DELETE')
  expect(onDone).toHaveBeenCalled()
})

it('does not delete when the confirmation is declined, and hides Delete on matched products', async () => {
  const user = userEvent.setup()
  vi.spyOn(window, 'confirm').mockReturnValue(false)
  const fetchMock = vi.fn()
  vi.stubGlobal('fetch', fetchMock)
  renderFix(unmatched)
  await user.click(screen.getByRole('button', { name: 'Delete' }))
  expect(fetchMock).not.toHaveBeenCalled()
  cleanup()
  renderFix(matched)
  expect(screen.queryByRole('button', { name: 'Delete' })).not.toBeInTheDocument()
})

it('renders the product_referenced refusal from the server detail', async () => {
  const user = userEvent.setup()
  vi.spyOn(window, 'confirm').mockReturnValue(true)
  vi.stubGlobal('fetch', vi.fn().mockResolvedValue(
    problemResponse(409, 'product_referenced', '3 entries reference this product - repoint or delete those entries first'),
  ))
  renderFix(unmatched)
  await user.click(screen.getByRole('button', { name: 'Delete' }))
  expect(await screen.findByRole('alert')).toHaveTextContent(/3 entries reference this product/i)
})
