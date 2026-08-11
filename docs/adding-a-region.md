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

## Collection tables

All in `services/collection/internal/server/regions.go`:

- `knownRegions`: the promotion target set and machinery key set.
- `regionSynonyms`: the reviewed free-text forms the normalize lever
  folds into the code (korea: "kr", "south korea"; brazil: "br",
  "brasil"; china: "cn"). Folding is lowercase-and-trim exact match,
  never fuzzy, and the canonical code folds via identity without a
  row.
- `regionChains`: release-date preference, the region's own IGDB
  territory first, then siblings, then worldwide (korea and china
  list asia as their sibling; brazil has none and falls from its own
  row to the scalar). A language-market row never joins a TV-standard
  region's chain: an IGDB korea date on an ntsc_j entry marks a
  localization launch, not the territory release.
- `localizationChains`: entry region to IGDB localization
  identifiers (korea reads ko-KR; china reads zh-CN then zh-TW;
  brazil reads pt-BR).
- `regionClass`: the pricing class. A market with no PriceCharting
  axis classes base through the switch default, and its copies price
  from the NA catalog as a deliberate proxy; the switch already
  answers for it, so the change is adding the region to the comment.

## Enrichment tables

- `knownRegions` and `regionSynonyms` twins in
  `services/enrichment/internal/server/regions.go`, row-identical to
  collection's. They scope the community-product normalize lever; a
  stale twin costs an unpromoted string, never a wrong write.
- The `countMatch` region label allowlist in
  `services/enrichment/internal/server/server.go`. Without the new
  code, match-outcome metrics for it clamp to the "none" label.
- `altTagFamilies` in `services/enrichment/internal/igdb/igdb.go`,
  when the region's native titles ride alternative_names comments
  instead of game_localizations rows: one prefix/exclude rule per
  localization identifier (zh-CN mines "Simplified Chinese title",
  zh-TW "Traditional Chinese title", pt-BR "Portuguese title", each
  excluding "translat" comments). Mining runs at projection time and
  alternative names are already in every stored raw, so a new family
  needs no refetch; it takes effect at the next reprojection.

## Frontend tables

- `Region` union and `REGIONS` in `frontend/src/lib/listParams.ts`.
  Order: TV-standard territories, then language markets, region_free
  last.
- `regionLabels` in `frontend/src/lib/regionLabels.ts`. The map is
  typed `Record<Region, MessageDescriptor>`, so the compiler forces
  this row. The label is a UI string: run `task gen` (it runs the
  lingui extract) and translate the new msgid in
  `frontend/src/locales/ja.po`.
- In `frontend/src/lib/productTitle.ts`: the `EntryRegion` union
  member, an `AVAILABILITY_REGIONS` identity row (search chips badge
  the region and the wizard's platform-first default can seed it), a
  `LOCALIZATION_CHAINS` row mirroring the collection table, a
  `REGION_LANGS` BCP-47 subtag (ko, zh, pt) for lang attributes on
  native-script entry text, and a `REGION_CLASS` base row (which also
  arms the region-mismatch banner against JP and PAL listings).
  `REGION_PLATFORMS` gets a row only for a platform that released in
  exactly that region. `ENTRY_REGION_ORDER` stays the TV-standard
  trio: a worldwide release row means the classic global markets and
  never implies a language-market release.

RegionPicker, the filter chips, and the URL param codec all derive
from `REGIONS` and pick the region up without edits.

## Contracts

The known values appear in prose descriptions, not enums: the Entry
region descriptions in `api/collection.yaml`, the CommunityMeta and
ResolveRequest region descriptions in `api/enrichment.yaml`, and the
`api/bff.yaml` mirrors of all of them, kept byte-consistent. Run
`task gen` after editing; CI fails on drift.

Two runbook lines name regions and grow with the set: the
match-outcomes metric label list in `docs/runbooks/enrichment.md` and
the chain prose in `docs/runbooks/collection.md`.

## Tests that enumerate the known set

These fail or go stale on graduation and are where the new region's
behavior gets pinned:

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
