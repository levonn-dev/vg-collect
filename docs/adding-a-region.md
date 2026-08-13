# Adding a region

Entry region is open-world on the wire: any non-empty string up to 32
runes is accepted at write time and stored verbatim. Adding a region
therefore never means widening a validation gate or running a
migration. It means graduating a value into the known set: a labeled
place in the pickers and filters, promotion from the normalize
levers, and rows in the per-region machinery tables. The levers then
promote any free-text spellings already stored. korea, brazil, and
china went through exactly this sequence; their rows sit next to
every step below as the worked example.

Ship the whole list as one change. The steps have no ordering
constraint between them, but a partial graduation leaves confusing
seams: a labeled region with no release-date chain renders the wrong
date, and a known region with no synonyms row promotes only entries
already spelling the code itself, while stored forms like "kr" or
"south korea" stay as typed.

The region vocabulary has two shapes. ntsc_u, ntsc_j, and pal are
TV-standard territories; region_free is the no-lockout catch-all;
korea, brazil, and china are language markets whose broadcast
standards do not place them in any of the first three buckets (Brazil
is PAL-M, a 60Hz NTSC-timing hybrid; Korea is NTSC; China broadcasts
PAL-D but its game region was always its own). New codes are
lowercase snake_case country or territory names.

## api/domain.yaml

Edit `api/domain.yaml`, then run `task gen` (its `gen:domain` step
runs first, ahead of the OpenAPI surfaces); CI fails on drift. This
one file generates every table both languages read - what used to be
a hand-synced Go/TS pair is now one edit:

- `regions[]`: add a `name` and a `class` (`base` | `jp` | `pal`, the
  pricing-listing class the region prices from). A market with no
  PriceCharting axis writes `class: base` explicitly - its copies
  price from the NA catalog as a deliberate proxy, so the yaml row
  states the choice rather than leaning on a fallback; only a region
  string `api/domain.yaml` never declares at all defaults to `base`
  at lookup time. Add `localization_chain` (ordered IGDB
  localization identifiers - korea: `[ko-KR]`, china: `[zh-CN,
  zh-TW]`) only when the region ships a localized presentation;
  ntsc_u and region_free have none, and the field is omitted from
  their rows entirely.
- `jp_console_names` and `platforms[]` cover the JP twin-platform
  machinery (Famicom/NES, Super Famicom/SNES) and PriceCharting's
  distinct-name JP consoles. A plain region graduation does not touch
  either.

Generates `libs/go/regionkit/tables_gen.go` (`RegionClass`,
`LocalizationChains`, `JPConsoleNames`, `ConsoleRegion`,
`PlatformRegions`, `TwinPlatformIDs`, `RegionNames`) and
`frontend/src/gen/domain.ts` (`REGIONS`, `Region`, `REGION_CLASS`,
`LOCALIZATION_CHAINS`, `JP_CONSOLE_NAMES`, `consoleRegionFor`,
`REGION_PLATFORMS`) via `tools/domaingen`. Both carry a "DO NOT EDIT"
header; edit the yaml, never the generated output by hand.

## regionkit

`libs/go/regionkit/regions.go` is hand-written, not generated:
`api/domain.yaml` holds only what both languages consume, and the
free-text fold vocabulary is Go-only (folding happens before either
service touches the value; both store the promoted code, never the
fold table itself). A graduation edits this file directly, alongside
`api/domain.yaml`:

- `KnownRegions`: the promotion target set and machinery key set,
  now one map shared by collection and enrichment instead of a
  per-service twin.
- `RegionSynonyms`: the reviewed free-text forms the normalize lever
  folds into the code (korea: "kr", "south korea"; brazil: "br",
  "brasil"; china: "cn"). Folding is lowercase-and-trim exact match,
  never fuzzy, and the canonical code folds via identity without a
  row.
- `RegionFoldMap()` builds fold -> canonical from both maps above;
  collection and enrichment both call `regionkit.RegionFoldMap()`
  directly, no local copy.

## Collection tables

`services/collection/internal/server/regions.go` keeps exactly one
table; its former known-region, synonym, localization-chain, and
pricing-class tables all generated away into `api/domain.yaml` and
regionkit:

- `regionChains`: release-date preference, the region's own IGDB
  territory first, then siblings, then worldwide (korea and china
  list asia as their sibling; brazil has none and falls from its own
  row to the scalar). A language-market row never joins a TV-standard
  region's chain: an IGDB korea date on an ntsc_j entry marks a
  localization launch, not the territory release. This chain is
  collection-only - nothing else reads it - so it stays hand-written
  rather than moving into `api/domain.yaml`.

## Enrichment tables

- `altTagFamilies` in `services/enrichment/internal/igdb/igdb.go`,
  when the region's native titles ride alternative_names comments
  instead of game_localizations rows: one prefix/exclude rule per
  localization identifier (zh-CN mines "Simplified Chinese title",
  zh-TW "Traditional Chinese title", pt-BR "Portuguese title", each
  excluding "translat" comments). Mining runs at projection time and
  alternative names are already in every stored raw, so a new family
  needs no refetch; it takes effect at the next reprojection.

No countMatch edit:
`services/enrichment/internal/server/server.go`'s match-outcome
metric clamps an unrecognized region to the "none" label by checking
`regionkit.KnownRegions` directly (`countMatch`), so the
`KnownRegions` edit above already covers it. Community-region
promotion (`InternalNormalizeCommunityRegions` in
`services/enrichment/internal/server/handlers_admin.go`) folds
through `regionkit.RegionFoldMap()` the same way collection's
normalize-regions lever does - no enrichment-local known-region or
synonym table left to twin against collection's.

Not part of a plain graduation: the one table left in
`services/enrichment/internal/server/regions.go`, `regionQueryChains`,
maps a region to the localization identifier PriceCharting needs for
provider-facing search. Only ntsc_j has a row - none of
korea/brazil/china needed one.

## Frontend tables

`REGIONS` and `Region` (re-exported from
`frontend/src/lib/listParams.ts`), and `LOCALIZATION_CHAINS`,
`REGION_PLATFORMS`, `consoleRegionFor` (re-exported from
`frontend/src/lib/productTitle.ts`, alongside `REGION_CLASS`,
consumed there by `regionMismatch` but not re-exported) generate
from `api/domain.yaml`; no manual edit. `JP_CONSOLE_NAMES` generates
too, but stays internal to `frontend/src/gen/domain.ts`, feeding
`consoleRegionFor`'s own distinct-name lookup there - no importer
outside that file.

Still hand-written, one edit each:

- `regionLabels` in `frontend/src/lib/regionLabels.ts`. The map is
  typed `Record<Region, MessageDescriptor>` against the generated
  `Region` union, so the compiler forces this row. The label is a UI
  string: run `task gen` (it runs the lingui extract) and translate
  the new msgid in `frontend/src/locales/ja.po`.
- In `frontend/src/lib/productTitle.ts`: the `EntryRegion` union
  member, an `AVAILABILITY_REGIONS` identity row (search chips badge
  the region and the wizard's platform-first default can seed it),
  and a `REGION_LANGS` BCP-47 subtag (ko, zh, pt) for lang attributes
  on native-script entry text. `ENTRY_REGION_ORDER` stays the
  TV-standard trio: a worldwide release row means the classic global
  markets and never implies a language-market release.
  `LOCALIZATION_CHAINS`, `REGION_PLATFORMS`, and `REGION_CLASS`
  generate into this file's import now - nothing to hand-edit for
  those three.

RegionPicker, the filter chips, and the URL param codec all derive
from `REGIONS` and pick the region up without edits.

## Contracts

Unrelated to `api/domain.yaml` above - these are hand-maintained
OpenAPI prose, not generated tables. The known values appear in
prose descriptions, not enums: the Entry region descriptions in
`api/collection.yaml`, the CommunityMeta and ResolveRequest region
descriptions in `api/enrichment.yaml`, and the `api/bff.yaml` mirrors
of all of them, kept byte-consistent. Run `task gen` after editing;
CI fails on drift.

Two runbook lines name regions and grow with the set: the
match-outcomes metric label list in `docs/runbooks/enrichment.md` and
the chain prose in `docs/runbooks/collection.md`.

## Tests that enumerate the known set

These fail or go stale on graduation and are where the new region's
behavior gets pinned:

- regionkit: `TestKnownRegions_ExactlySevenRegions` in
  `regions_test.go` (bump the count and add the region to its want
  list) and `TestRegionClass_AllSevenRegions` in
  `tables_gen_test.go` (add the region's expected class - a
  hand-written pin against the generator's output, so an
  `api/domain.yaml` edit alone does not satisfy it).
- collection: `TestNormalizeRegions_PromotesAndRepicks` (promotion
  arms), `TestPickReleaseDate` and `TestPickLocalization` (chains),
  `TestConsoleRegionClassification` (pricing class).
- enrichment:
  `TestUnitInternalNormalizeCommunityRegions_PromotesFoldMatchSkipsUnknown`,
  `TestUnitTelemetry_MatchOutcomesCarriesRegionLabel` (label
  allowlist), `TestBundleLocalizations` (mining families).
- frontend: `regionLabels.test.ts`, `RegionPicker.test.tsx`,
  `DetailsStep.test.tsx` (full option lists), `productTitle.test.ts`
  (every table above asserts its exact contents).
- e2e: the Other-regions option list in
  `frontend/e2e/region-locale.spec.ts`.

A fixture rule that keeps these honest: tests that need a region
outside the known set use a spelling that stays outside it (taiwan,
moon_base_region), so graduating a real region cannot silently
invert their premise.

## After deploy

Nothing migrates. Free-text spellings already stored promote on the
nightly jobs - collection's normalize-platforms, normalize-regions,
rematch chain at 07:00 and enrichment's community-region step on the
06:00 refresh job - or immediately from the Admin page's normalize
buttons. Promotion re-picks a game-backed entry's release date and
localized snapshot inline, so promoted rows need no follow-up.

Two cases want an extra lever run:

- A new `altTagFamilies` row: trigger a catalog refresh (Admin page,
  or wait for 06:00) so reprojection builds the new bundles, then the
  entry resnapshot (Admin page card, or `POST /internal/resnapshot`
  on collection) re-derives localized fields for entries that already
  exist.
- Entries that already carry the canonical code, created before the
  region's chains landed: resnapshot alone.

## Display rules that hold for every region

- The translit title form (what the en locale uses) never renders
  native script. A name-only bundle - the ko-KR norm, since IGDB
  rarely carries Korean romanizations - keeps the canonical title,
  and the native name renders on the entry's secondary line with its
  own lang attribute.
- A market with no PriceCharting axis prices from base listings, and
  the region-mismatch banner flags a JP or PAL listing hand-picked
  onto such an entry.
- UI locales are a separate axis. Adding a region never requires
  adding a locale; `docs/translations.md` covers that path.
