import { devFixtures, providerNames } from './providers'

// Locks the exact data Login, Account, and site.ts all render from -
// a change here is a deliberate change to sign-in options everywhere,
// not an accident in one file.
it('names the two live providers', () => {
  expect(providerNames).toEqual({ google: 'Google', twitch: 'Twitch' })
})

it('lists the dev fixture users in display order', () => {
  expect(devFixtures).toEqual(['alice', 'bob', 'admin'])
})
