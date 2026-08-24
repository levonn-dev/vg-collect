import { ApiError } from './client'
import { fetchFxRates } from './fx'
import { calledPath, fxRatesFixture, jsonResponse, problemResponse } from '../test/fixtures'

afterEach(() => vi.unstubAllGlobals())

it('fetchFxRates reads the rates', async () => {
  const fetchMock = vi.fn().mockResolvedValue(jsonResponse(200, fxRatesFixture()))
  vi.stubGlobal('fetch', fetchMock)
  const rates = await fetchFxRates()
  expect(rates).toEqual(fxRatesFixture())
  expect(calledPath(fetchMock)).toBe('/api/fx')
})

it('maps problem+json onto ApiError through unwrap', async () => {
  vi.stubGlobal('fetch', vi.fn().mockResolvedValue(problemResponse(502, 'upstream_unavailable')))
  const err = await fetchFxRates().catch((e: unknown) => e)
  expect(err).toBeInstanceOf(ApiError)
  expect((err as ApiError).status).toBe(502)
  expect((err as ApiError).code).toBe('upstream_unavailable')
})
