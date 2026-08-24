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
    npm run gen          regenerate src/api/schema.ts from api/bundled/bff.yaml

## API client

`src/api/client.ts` wraps `openapi-fetch` (the `api` client, typed
against `src/api/schema.ts`) with `unwrap`, which turns its
`{ data, error, response }` result into a throw-on-problem contract:
a parsed RFC 9457 problem body rejects with `ApiError` (fields
`status` and `code`; the detail text becomes its `.message`), any
other non-ok response rejects with a bare `ApiError(status)`, and a
204 resolves `undefined`. `fetch` resolves through `globalThis` on
every call instead of being captured at module init, so OTel's
`window.fetch` patch, which lands after this module loads, still
wraps every request.

Domain modules under `src/api/` (`me`, `catalog`, `collection`,
`social`, `submissions`, `admin`, `platforms`, `fx`) hold the actual
calls - things like `api.GET('/api/products/{productId}', ...)` - as
path literals checked at compile time against `schema.ts`: a path or
parameter that drifts from `api/bff.yaml` fails the build instead of
the request.

Three files regenerate from the OpenAPI contracts and are committed
like any other source file: `src/api/schema.ts` from
`api/bundled/bff.yaml` (`npm run gen`, openapi-typescript with
`--enum-values`), and `src/gen/domain.ts` plus `src/gen/facets.ts`
from `api/domain.yaml` and `api/bundled/bff.yaml` respectively
(`task gen:domain`, the Go tool `tools/domaingen`). Constraint values
split by kind, not by file role: every enum's value list - the option
arrays a form or a filter reads - comes from `schema.ts`'s generated
`*Values` exports (one per named vocabulary schema or inline enum,
e.g. `itemTypeValues`); every OTHER constraint - caps, numeric bounds,
defaults, the handle pattern - comes from `facets.ts` instead,
mirrored straight out of the bundle's schemas and parameters rather
than a hand-maintained constants module. The root `task gen` runs
both generators, and a stale output fails CI's drift check the same
way a stale locale catalog does.

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
translatable copy. This rewrites every registered locale's catalog:
new English text lands in `en.po` and shows up as a missing entry in
each translated catalog, which then falls back to English until
someone translates it. All of them are committed like any other source
file. The root `task gen` runs the same extraction, so a catalog left
out of date fails CI's drift check the same way a stale generated API
client would.

Prose pages (About, Terms, Privacy, Help) skip the catalog entirely.
Each is a whole page per locale under `src/pages/<page>/` (for example
`src/pages/about/About.en.tsx` and `About.ja.tsx`), picked by
`ProsePage` at render time. Translating one of these means adding a
page variant, not catalog entries.

Adding a locale takes a `.po` file under `src/locales/`, an entry in
`SUPPORTED_LOCALES` and `LOCALE_NAMES` in `src/lib/locales.ts`, and
one loader line in `CATALOG_LOADERS` in `src/lib/locale.ts` (the split
keeps the Lingui CLI config loadable under plain Node - see the
comments in both files). Prose variants are optional and can follow
per page. English and Japanese ship today; the language switcher in
the footer renders nothing while only one locale is supported, then
appears on its own. The footer is its single mount point and renders
in both shells, so signed-out visitors can switch languages on the
public pages too. See `docs/translations.md` for the
contributor-facing guide.

Product titles and cover art sit outside all of the above: they are IGDB
data, picked per region, not catalog strings, so none of this machinery
translates them. A locale only picks which FORM of that data to show -
`titleFormFor` in `src/lib/productTitle.ts` maps a locale to `translit`
(romanized) or `native` (original script); an unmapped locale defaults
to `translit`. A new locale that wants the other default adds one row to
that table. Search-result platform chips are the region picker: each
lists the entry regions its release actually shipped in
(`AVAILABILITY_REGIONS` and `platformEntryRegions` in the same module; a
worldwide release lists all of them), the picked chip's set leads the
wizard's grouped Region select, and `LOCALIZATION_CHAINS` gives the
details heading its region-appropriate title. Each chip's own suggested
region tries the search result's matched region first, then the UI
locale's home region when that region is in the chip's set, then the
earliest release region. Hardware and pc_listing search rows carry a
region tag of their own, derived from the listing's console-name axis
(`consoleRegionFor` in the same module: a "PAL " prefix, a "JP " prefix,
or a Famicom-family distinct name maps to pal/ntsc_j, everything else to
ntsc_u); a hardware pick seeds the wizard's region default with that
tag. Wherever a pick carries no region signal at all, the wizard's
default falls back to a hardcoded `ntsc_u` (`defaultDetails` in
`src/components/wizard/DetailsStep.tsx`) - it never consults the UI
locale. `LOCALE_HOME_REGIONS` only feeds the per-chip suggestion
described above, and only when the chip's own region set already
contains the home region.

See the root README for the full stack (task run / task down).
