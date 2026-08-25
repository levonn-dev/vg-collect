// The one list a new locale is added to (also needs a LOCALE_NAMES
// entry below and a CATALOG_LOADERS loader in locale.ts). Lives apart
// from locale.ts because the Lingui CLI runs under plain Node, which
// can't resolve locale.ts's static `.po` import (no Vite plugin there).
export const SUPPORTED_LOCALES = ['en', 'ja'] as const
export type Locale = (typeof SUPPORTED_LOCALES)[number]

// Endonyms, never translated: a German speaker on a Japanese device
// must still find "Deutsch".
export const LOCALE_NAMES: Record<Locale, string> = { en: 'English', ja: '日本語' }
