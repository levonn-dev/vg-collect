// Proper nouns only (leave-alone list) - never wrapped for translation.
// An unknown provider id falls back to itself. Shared by Login and
// Account (button labels) and site.ts (the about/privacy/terms pages'
// provider list), so the three cannot drift apart.
export const providerNames: Record<string, string> = {
  google: 'Google',
  twitch: 'Twitch',
}

// The dev-only sign-in stand-in's fixture users; Login and Account
// each render one link-button per name. The identical list also
// exists in Go (the auth service's dev provider) - a deliberate twin
// across the language boundary, not something this module can unify.
export const devFixtures = ['alice', 'bob', 'admin']
