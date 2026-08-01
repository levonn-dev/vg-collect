// The one list a new locale is added to. lingui.config.ts imports it
// directly, so extraction, compiled catalogs, and the UI cannot
// disagree. Adding a locale also means a LOCALE_NAMES entry below and
// a CATALOG_LOADERS loader in locale.ts - see the comment there.
//
// This lives apart from locale.ts on purpose. lingui.config.ts is
// loaded by the Lingui CLI under plain Node (no Vite plugin there to
// turn a `.po` import into a JS module), and locale.ts statically
// imports the en catalog for boot resilience (see activateBoot in
// locale.ts) - a `.po` specifier Node cannot resolve on its own.
// Keeping this list in a module with no such import keeps the CLI
// config loadable.
export const SUPPORTED_LOCALES = ['en'] as const
export type Locale = (typeof SUPPORTED_LOCALES)[number]

// Endonyms: each language's name for itself, shown in the switcher.
// Never translated - a German speaker hunting the switcher on a
// device set to Japanese must be able to find "Deutsch".
export const LOCALE_NAMES: Record<Locale, string> = { en: 'English' }
