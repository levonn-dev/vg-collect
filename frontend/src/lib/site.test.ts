import { site } from './site'

afterEach(() => {
  vi.unstubAllEnvs()
})

it('applies defaults when nothing is set', () => {
  const s = site()
  expect(s.name).toBe('vgkeep')
  expect(s.sourceUrl).toBe('https://github.com/levonn-dev/vgkeep')
  expect(s.operator).toBe('')
  expect(s.contact).toBe('')
  expect(s.jurisdiction).toBe('')
  expect(s.dataSources).toEqual([])
  expect(s.authProviders).toEqual([])
})

it('returns identity slots verbatim when set', () => {
  vi.stubEnv('VITE_SITE_NAME', 'MyShelf')
  vi.stubEnv('VITE_SITE_OPERATOR', 'Sam')
  vi.stubEnv('VITE_SITE_CONTACT', 'sam@example.test')
  vi.stubEnv('VITE_SITE_JURISDICTION', 'Germany')
  vi.stubEnv('VITE_SITE_SOURCE_URL', 'https://example.test/fork')
  const s = site()
  expect(s.name).toBe('MyShelf')
  expect(s.operator).toBe('Sam')
  expect(s.contact).toBe('sam@example.test')
  expect(s.jurisdiction).toBe('Germany')
  expect(s.sourceUrl).toBe('https://example.test/fork')
})

it('resolves data sources from the CSV in catalog order', () => {
  vi.stubEnv('VITE_SITE_DATA_SOURCES', 'frankfurter, igdb')
  expect(site().dataSources.map((d) => d.key)).toEqual(['igdb', 'frankfurter'])
})

it('resolves full catalog records', () => {
  vi.stubEnv('VITE_SITE_DATA_SOURCES', 'pricecharting')
  expect(site().dataSources).toEqual([
    {
      key: 'pricecharting',
      label: 'PriceCharting',
      dataType: 'Price data',
      url: 'https://www.pricecharting.com',
    },
  ])
})

it('ignores unknown CSV keys', () => {
  vi.stubEnv('VITE_SITE_DATA_SOURCES', 'igdb,steam,none')
  expect(site().dataSources.map((d) => d.key)).toEqual(['igdb'])
})

it('treats an explicitly empty CSV as none', () => {
  vi.stubEnv('VITE_SITE_DATA_SOURCES', '')
  vi.stubEnv('VITE_SITE_AUTH_PROVIDERS', '')
  expect(site().dataSources).toEqual([])
  expect(site().authProviders).toEqual([])
})

it('resolves auth providers with labels', () => {
  vi.stubEnv('VITE_SITE_AUTH_PROVIDERS', 'twitch,google')
  expect(site().authProviders).toEqual([
    { key: 'google', label: 'Google' },
    { key: 'twitch', label: 'Twitch' },
  ])
})

it('never resolves a dev provider', () => {
  vi.stubEnv('VITE_SITE_AUTH_PROVIDERS', 'dev,google')
  expect(site().authProviders.map((p) => p.key)).toEqual(['google'])
})
