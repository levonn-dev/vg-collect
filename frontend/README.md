# vgkeep frontend

React 19 SPA for the vgkeep video game collection tracker.
Typed against the BFF OpenAPI contract at `api/bff.yaml`.
Served in production by the BFF at the same origin; in dev the Vite
proxy forwards `/api` to the APISIX gateway port-forward on :8090.

Site identity (the VITE_SITE_* slots, footer credit lines) is baked in
at image build; the dev server on :5173 runs without the values Tilt
derives for the cluster image, so credits and operator text are absent
there by design.

## Dev commands

    npm run dev          start the Vite dev server on :5173
    npm run test         vitest (unit, jsdom)
    npm run test:cover   vitest + 80% coverage gate
    npm run lint         eslint
    npm run build        tsc + vite build
    npm run gen          regenerate src/api/schema.d.ts from api/bff.yaml

## Translations

UI strings are authored in English through Lingui macros, not as raw
JSX text. Three shapes cover the app:

Sentences and paragraphs use `Trans` in JSX:

```tsx
// before
<h2 className="mb-2 text-2xl font-bold">Page not found</h2>
// after
<h2 className="mb-2 text-2xl font-bold"><Trans>Page not found</Trans></h2>
```

Attribute strings (`aria-label`, `placeholder`, `title`) use `t` from
`useLingui()`:

```tsx
const { t } = useLingui()
<main aria-label={t`Page not found`} ...>
```

Module-level string tables (constants outside a component) use `msg`
descriptors, rendered with `i18n._()` where they're used. From
`src/lib/site.ts`:

```ts
import { msg } from '@lingui/core/macro'
const DATA_SOURCES = [
  { key: 'igdb', label: 'IGDB', dataType: msg`Game data`, url: 'https://www.igdb.com' },
  // ...
]
// at the render site:
const { i18n } = useLingui()
i18n._(dataSource.dataType)
```

Run `npm run extract` (or `task frontend:extract`) after changing any
translatable copy. This regenerates `src/locales/en.po`, which is
committed like any other source file. The root `task gen` runs the
same extraction, so a catalog left out of date fails CI's drift check
the same way a stale generated API client would.

Prose pages (About, Terms, Privacy, Help) skip the catalog entirely.
Each is a whole page per locale under `src/pages/<page>/` (for example
`src/pages/about/About.en.tsx`), picked by `ProsePage` at render time.
Translating one of these means adding a page variant, not catalog
entries.

Adding a locale takes an entry in `SUPPORTED_LOCALES`, `LOCALE_NAMES`,
and `CATALOG_LOADERS` in `src/lib/locale.ts`, plus a `.po` file under
`src/locales/`. The language switcher in the app bar renders nothing
until two locales are supported, then appears on its own. See
`docs/translations.md` for the contributor-facing guide.

See the root README for the full stack (task run / task down).
