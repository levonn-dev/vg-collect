import { ackSubmissionResolution, cancelSubmission, createSubmission, fetchSubmission } from './submissions'

const jsonResponse = (status: number, body: unknown) =>
  new Response(JSON.stringify(body), { status, headers: { 'Content-Type': 'application/json' } })

afterEach(() => vi.unstubAllGlobals())

const sub = { id: 's1', entry_id: 'e1', status: 'pending', created_at: 'x', updated_at: 'x' }

it('fetchSubmission reads the entry submission', async () => {
  const fetchMock = vi.fn().mockResolvedValue(jsonResponse(200, sub))
  vi.stubGlobal('fetch', fetchMock)
  await fetchSubmission('e1')
  expect(fetchMock).toHaveBeenCalledWith('/api/entries/e1/submission')
})

it('createSubmission posts without a body', async () => {
  const fetchMock = vi.fn().mockResolvedValue(jsonResponse(201, sub))
  vi.stubGlobal('fetch', fetchMock)
  await createSubmission('e1')
  expect(fetchMock.mock.calls[0][0]).toBe('/api/entries/e1/submission')
  expect(fetchMock.mock.calls[0][1]).toMatchObject({ method: 'POST' })
})

it('cancelSubmission deletes and resolves on 204', async () => {
  const fetchMock = vi.fn().mockResolvedValue(new Response(null, { status: 204 }))
  vi.stubGlobal('fetch', fetchMock)
  await cancelSubmission('e1')
  expect(fetchMock.mock.calls[0][1]).toMatchObject({ method: 'DELETE' })
})

it('ackSubmissionResolution posts to the ack path', async () => {
  const fetchMock = vi.fn().mockResolvedValue(new Response(null, { status: 204 }))
  vi.stubGlobal('fetch', fetchMock)
  await ackSubmissionResolution('e1')
  expect(fetchMock.mock.calls[0][0]).toBe('/api/entries/e1/submission/ack')
  expect(fetchMock.mock.calls[0][1]).toMatchObject({ method: 'POST' })
})
