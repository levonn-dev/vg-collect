import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { screen } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { useState } from 'react'
import { jsonResponse, problemResponse } from '../../test/fixtures'
import { renderWithI18n } from '../../test/i18n'
import TagPicker from './TagPicker'

function Harness({ initial = [] as string[] }) {
  const [ids, setIds] = useState<string[]>(initial)
  return <TagPicker value={ids} onChange={setIds} />
}

function renderPicker(initial?: string[]) {
  const qc = new QueryClient({ defaultOptions: { queries: { retry: false } } })
  return renderWithI18n(
    <QueryClientProvider client={qc}>
      <Harness initial={initial} />
    </QueryClientProvider>,
  )
}

afterEach(() => vi.unstubAllGlobals())

const tags = [
  { id: 't1', name: 'rpg', entry_count: 3 },
  { id: 't2', name: 'snes', entry_count: 1 },
]

it('lists tags and toggles assignment', async () => {
  vi.stubGlobal('fetch', vi.fn().mockResolvedValue(jsonResponse(200, { tags })))
  renderPicker(['t2'])
  const rpg = await screen.findByRole('checkbox', { name: /rpg/ })
  expect(screen.getByRole('checkbox', { name: /snes/ })).toBeChecked()
  await userEvent.click(rpg)
  expect(rpg).toBeChecked()
})

it('creates a tag inline and selects it', async () => {
  const fetchMock = vi.fn()
    .mockResolvedValueOnce(jsonResponse(200, { tags }))
    .mockResolvedValueOnce(jsonResponse(201, { id: 't3', name: 'holiday', entry_count: 0 }))
    .mockResolvedValueOnce(jsonResponse(200, { tags: [...tags, { id: 't3', name: 'holiday', entry_count: 0 }] }))
  vi.stubGlobal('fetch', fetchMock)
  renderPicker()
  await screen.findByRole('checkbox', { name: /rpg/ })
  await userEvent.type(screen.getByRole('textbox', { name: /new tag/i }), 'holiday')
  await userEvent.click(screen.getByRole('button', { name: /add tag/i }))
  expect(await screen.findByRole('checkbox', { name: /holiday/ })).toBeChecked()
})

it('surfaces a duplicate-name conflict as its curated tag_exists message', async () => {
  const fetchMock = vi.fn()
    .mockResolvedValueOnce(jsonResponse(200, { tags }))
    .mockResolvedValueOnce(problemResponse(409, 'tag_exists', 'a tag with that name already exists'))
  vi.stubGlobal('fetch', fetchMock)
  renderPicker()
  await screen.findByRole('checkbox', { name: /rpg/ })
  await userEvent.type(screen.getByRole('textbox', { name: /new tag/i }), 'rpg')
  await userEvent.click(screen.getByRole('button', { name: /add tag/i }))
  expect(await screen.findByRole('alert')).toHaveTextContent('A tag with that name already exists.')
})

it('shows the generic fallback message when tag creation fails with no recognized code', async () => {
  const fetchMock = vi.fn()
    .mockResolvedValueOnce(jsonResponse(200, { tags }))
    .mockRejectedValueOnce(new TypeError('Failed to fetch'))
  vi.stubGlobal('fetch', fetchMock)
  renderPicker()
  await screen.findByRole('checkbox', { name: /rpg/ })
  await userEvent.type(screen.getByRole('textbox', { name: /new tag/i }), 'holiday')
  await userEvent.click(screen.getByRole('button', { name: /add tag/i }))
  expect(await screen.findByRole('alert')).toHaveTextContent('The tag could not be created.')
})
