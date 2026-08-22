import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { fireEvent, screen } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { triggerRefresh, triggerRematch } from '../../api/admin'
import { calledPath, jsonResponse, problemResponse } from '../../test/fixtures'
import { renderWithI18n } from '../../test/i18n'
import NormalizeTrigger, { normalizeSuccessMessage } from './NormalizeTrigger'

function renderNormalize(props: {
  title: string
  actionLabel: string
  mutationFn: () => Promise<{ scanned: number; normalized: number; skipped: number }>
}) {
  const qc = new QueryClient({ defaultOptions: { mutations: { retry: false } } })
  renderWithI18n(
    <QueryClientProvider client={qc}>
      <NormalizeTrigger {...props} successMessage={normalizeSuccessMessage} />
    </QueryClientProvider>,
  )
}

test('runs the sweep and renders counts', async () => {
  const fn = vi.fn().mockResolvedValue({ scanned: 3, normalized: 2, skipped: 1 })
  renderNormalize({
    title: 'Normalize regions',
    actionLabel: 'Run region normalization',
    mutationFn: fn,
  })
  fireEvent.click(screen.getByRole('button', { name: 'Run region normalization' }))
  expect(await screen.findByText('Scanned 3, promoted 2, skipped 1.')).toBeInTheDocument()
})

test('failure renders the alert', async () => {
  const fn = vi.fn().mockRejectedValue(new Error('boom'))
  renderNormalize({
    title: 'T',
    actionLabel: 'Run',
    mutationFn: fn,
  })
  fireEvent.click(screen.getByRole('button', { name: 'Run' }))
  expect(await screen.findByRole('alert')).toBeInTheDocument()
})

// Merged from the deleted RefreshTrigger.test.tsx: Admin.tsx now
// configures NormalizeTrigger directly for the catalog-refresh lever,
// so these tests exercise that same configuration instead of a
// dedicated RefreshTrigger component.
function renderRefresh() {
  const qc = new QueryClient({ defaultOptions: { mutations: { retry: false } } })
  renderWithI18n(
    <QueryClientProvider client={qc}>
      <NormalizeTrigger
        title="Catalog refresh"
        actionLabel="Trigger catalog refresh"
        mutationFn={triggerRefresh}
        successMessage={() => 'Refresh started.'}
        inProgressCode="refresh_in_progress"
        inProgressMessage="A refresh is already running."
        failureMessage="The trigger failed - try again."
      />
    </QueryClientProvider>,
  )
}

afterEach(() => vi.unstubAllGlobals())

it('starts a catalog refresh', async () => {
  const user = userEvent.setup()
  const fetchMock = vi.fn().mockResolvedValue(jsonResponse(202, { status: 'started' }))
  vi.stubGlobal('fetch', fetchMock)
  renderRefresh()
  await user.click(screen.getByRole('button', { name: 'Trigger catalog refresh' }))
  expect(await screen.findByText('Refresh started.')).toBeInTheDocument()
  expect(calledPath(fetchMock, 0)).toBe('/api/admin/refresh')
})

it('reports an already-running refresh', async () => {
  const user = userEvent.setup()
  vi.stubGlobal('fetch', vi.fn().mockResolvedValue(problemResponse(409, 'refresh_in_progress', 'a refresh is already running')))
  renderRefresh()
  await user.click(screen.getByRole('button', { name: 'Trigger catalog refresh' }))
  expect(await screen.findByRole('alert')).toHaveTextContent('A refresh is already running.')
})

it('falls back to the generic refresh failure message on a non-conflict error', async () => {
  const user = userEvent.setup()
  vi.stubGlobal('fetch', vi.fn().mockResolvedValue(problemResponse(500, 'internal')))
  renderRefresh()
  await user.click(screen.getByRole('button', { name: 'Trigger catalog refresh' }))
  expect(await screen.findByRole('alert')).toHaveTextContent('The trigger failed - try again.')
})

// Merged from the deleted RematchTrigger.test.tsx: same reasoning as
// renderRefresh above, for the entry-rematch lever.
function renderRematch() {
  const qc = new QueryClient({ defaultOptions: { mutations: { retry: false } } })
  renderWithI18n(
    <QueryClientProvider client={qc}>
      <NormalizeTrigger
        title="Entry rematch"
        actionLabel="Trigger entry rematch"
        mutationFn={triggerRematch}
        successMessage={() => 'Rematch started.'}
        inProgressCode="rematch_in_progress"
        inProgressMessage="A rematch is already running."
        failureMessage="The rematch trigger failed - try again."
      />
    </QueryClientProvider>,
  )
}

it('starts an entry rematch', async () => {
  const user = userEvent.setup()
  const fetchMock = vi.fn().mockResolvedValue(jsonResponse(202, { status: 'started' }))
  vi.stubGlobal('fetch', fetchMock)
  renderRematch()
  await user.click(screen.getByRole('button', { name: 'Trigger entry rematch' }))
  expect(await screen.findByText('Rematch started.')).toBeInTheDocument()
  expect(calledPath(fetchMock, 0)).toBe('/api/admin/rematch')
})

it('reports an already-running rematch', async () => {
  const user = userEvent.setup()
  vi.stubGlobal('fetch', vi.fn().mockResolvedValue(problemResponse(409, 'rematch_in_progress', 'a rematch is already running')))
  renderRematch()
  await user.click(screen.getByRole('button', { name: 'Trigger entry rematch' }))
  expect(await screen.findByRole('alert')).toHaveTextContent('A rematch is already running.')
})

it('falls back to the generic rematch failure message on a non-conflict error', async () => {
  const user = userEvent.setup()
  vi.stubGlobal('fetch', vi.fn().mockResolvedValue(problemResponse(500, 'internal')))
  renderRematch()
  await user.click(screen.getByRole('button', { name: 'Trigger entry rematch' }))
  expect(await screen.findByRole('alert')).toHaveTextContent('The rematch trigger failed - try again.')
})
