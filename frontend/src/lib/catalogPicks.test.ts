import type { SearchResult } from '../api/catalog'
import { communityPickOf } from './catalogPicks'

const communityResult: SearchResult = {
  type: 'game',
  name: 'Repro Alpha',
  origin: 'community',
  product_id: 'c0ffee00-0000-4000-8000-000000000001',
  item_type: 'game',
  platform_name: 'SNES',
  cover_url: 'https://img.example/ra.jpg',
  first_release_date: '1994-01-01',
  region: 'ntsc_j',
  developers: ['Squaresoft'],
  publishers: ['Square'],
}

it('builds a community pick carrying all ten fields from a search result', () => {
  expect(communityPickOf(communityResult)).toEqual({
    kind: 'community',
    productId: 'c0ffee00-0000-4000-8000-000000000001',
    name: 'Repro Alpha',
    itemType: 'game',
    platformName: 'SNES',
    coverUrl: 'https://img.example/ra.jpg',
    firstReleaseDate: '1994-01-01',
    region: 'ntsc_j',
    developers: ['Squaresoft'],
    publishers: ['Square'],
  })
})

it('defaults itemType to game when the result omits it', () => {
  const result: SearchResult = {
    type: 'game', name: 'Repro Beta', product_id: 'c0ffee00-0000-4000-8000-000000000002',
  }
  expect(communityPickOf(result).itemType).toBe('game')
})
