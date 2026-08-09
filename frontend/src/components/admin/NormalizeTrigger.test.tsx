import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { fireEvent, screen } from '@testing-library/react'
import { renderWithI18n } from '../../test/i18n'
import NormalizeTrigger from './NormalizeTrigger'

function renderNormalize(props: { title: string; actionLabel: string; mutationFn: () => Promise<{ scanned: number; normalized: number; skipped: number }> }) {
  const qc = new QueryClient({ defaultOptions: { mutations: { retry: false } } })
  renderWithI18n(
    <QueryClientProvider client={qc}>
      <NormalizeTrigger {...props} />
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
