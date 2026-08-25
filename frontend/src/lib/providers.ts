// Proper nouns only, never wrapped for translation; unknown id falls
// back to itself. Shared by Login, Account, and site.ts, so the three cannot drift.
export const providerNames: Record<string, string> = {
  google: 'Google',
  twitch: 'Twitch',
}

// Dev-only fixture users; Login/Account render one link-button per
// name. Mirrored in Go (auth service's dev provider), a deliberate twin.
export const devFixtures = ['alice', 'bob', 'admin']
