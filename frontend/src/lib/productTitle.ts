// Pure display-selection helpers for region-localized product titles.
// No React, no Lingui: callers own rendering and translation, and this
// module only picks which wire field to show for a given title form.

export type TitleForm = 'translit' | 'native'

// Which title form a UI locale defaults to: translit (Latin-script
// romanization) for locales that read Latin script natively, native
// (original script) for locales that read the source script directly.
// Extend this table when a new UI locale ships with a different
// default; an unmapped locale falls back to translit.
const LOCALE_TITLE_FORMS: Record<string, TitleForm> = {
  en: 'translit',
  ja: 'native',
}

export function titleFormFor(locale: string): TitleForm {
  return LOCALE_TITLE_FORMS[locale] ?? 'translit'
}

// LocalizedTitled is the subset of entry/search-result wire fields
// this module reads. It is structural rather than imported from the
// generated schema, so it fits both a collection entry and a search
// result's picked localization bundle without either shape needing to
// import the other's generated type.
export interface LocalizedTitled {
  display_name: string
  localized_name?: string
  localized_name_translit?: string
  localized_cover_url?: string
  cover_url?: string
  region?: string
}

// entryTitle picks the title text for the given form. translit
// prefers the romanized name and otherwise falls straight to the
// canonical display_name - never the native script, so a Latin-
// preferring locale is never fronted by CJK (the skipped native name
// surfaces on the secondary line instead). native prefers the
// original-script name, falling back through the transliteration
// (still the localized identity, and already Latin) before the
// canonical name.
export function entryTitle(e: LocalizedTitled, form: TitleForm): string {
  if (form === 'translit') return e.localized_name_translit ?? e.display_name
  return e.localized_name ?? e.localized_name_translit ?? e.display_name
}

// entrySecondary is the counterpart line under the title: the
// canonical display_name when the title is localized, or the native-
// script name the translit form skipped when the title fell back to
// canonical; omitted when it would just repeat entryTitle.
export function entrySecondary(e: LocalizedTitled, form: TitleForm): string | undefined {
  const title = entryTitle(e, form)
  if (title !== e.display_name) return e.display_name
  return e.localized_name !== undefined && e.localized_name !== title ? e.localized_name : undefined
}

// entrySecondaryLang tags the secondary line's language - set only
// when that line carries the native-script name (the canonical name
// needs no tag).
export function entrySecondaryLang(e: LocalizedTitled, form: TitleForm): string | undefined {
  const secondary = entrySecondary(e, form)
  if (secondary === undefined || secondary === e.display_name) return undefined
  return e.region ? REGION_LANGS[e.region] : undefined
}

// entryCover prefers the region-localized box art over the entry's
// own cover_url.
export function entryCover(e: LocalizedTitled): string | undefined {
  return e.localized_cover_url ?? e.cover_url
}

// REGION_LANGS maps an entry region to the BCP-47 language subtag of
// its localized text. Extend this table when a new entry region gets
// a localized bundle; ntsc_u, pal, and region_free carry none today
// (pal bundles are cover-only). china stays the bare zh subtag: its
// chain can resolve either script, and the subtag names the language,
// not the script variant.
export const REGION_LANGS: Record<string, string> = {
  ntsc_j: 'ja',
  korea: 'ko',
  china: 'zh',
  brazil: 'pt',
}

// Names which field actually supplied entryTitle's value, following
// the same precedence: entryTitleLang needs to know whether the
// rendered text is localized at all, and if so whether it is native
// script or a Latin transliteration, before it can attach a lang
// subtag.
type TitleSource = 'native' | 'translit' | 'canonical'

function titleSource(e: LocalizedTitled, form: TitleForm): TitleSource {
  if (form === 'translit') {
    if (e.localized_name_translit !== undefined) return 'translit'
    return 'canonical'
  }
  if (e.localized_name !== undefined) return 'native'
  if (e.localized_name_translit !== undefined) return 'translit'
  return 'canonical'
}

// entryTitleLang is the lang attribute for entryTitle's chosen text,
// or undefined when that text is not localized: the canonical
// display_name, or a localized field whose region has no entry in
// REGION_LANGS. The tag names the language of whatever is actually on
// screen, so it follows the field entryTitle picked - which can
// differ from the requested form when that form's field was missing
// and entryTitle fell back to the other localized field. Native
// script reports the bare subtag; a Latin transliteration reports the
// -Latn script-tagged form, since the rendered text is Latin script
// even though its language is the subtag's.
export function entryTitleLang(e: LocalizedTitled, form: TitleForm): string | undefined {
  const source = titleSource(e, form)
  if (source === 'canonical') return undefined
  const subtag = e.region ? REGION_LANGS[e.region] : undefined
  if (!subtag) return undefined
  return source === 'translit' ? `${subtag}-Latn` : subtag
}

// Leading language subtag of a locale-form region identifier: 2-3
// lowercase letters immediately before a dash ("ja-JP" -> "ja",
// "ko-KR" -> "ko"). Continent-form identifiers ("EU") carry no
// language and match nothing.
const LOCALE_FORM_LANG = /^[a-z]{2,3}(?=-)/

// bundleLang extracts a localization bundle's language subtag from
// its region identifier (a search result's matched_region, or a
// Localization's own region), as opposed to REGION_LANGS above, which
// maps an entry's own region enum instead of this free-form
// identifier space.
export function bundleLang(regionIdentifier: string): string | undefined {
  return LOCALE_FORM_LANG.exec(regionIdentifier)?.[0]
}

// REGION_FROM_MATCH maps a matched_region identifier (the same
// free-form identifier space bundleLang reads) to the entry region enum
// a picked game should default to - only identifiers with an
// unambiguous entry-region counterpart are listed. Extend this table
// when another identifier gets one; an unmapped identifier (ko-KR
// today) yields no suggestion, and the wizard keeps its own default.
export const REGION_FROM_MATCH: Record<string, 'ntsc_u' | 'ntsc_j' | 'pal' | 'region_free'> = {
  'ja-JP': 'ntsc_j',
  EU: 'pal',
}

// AVAILABILITY_REGIONS maps canonical IGDB release regions onto entry
// regions for platformEntryRegions below. worldwide is handled there
// by expanding to the TV-standard trio, rather than by a table row.
// The language-market regions (korea, brazil, china) map to
// themselves: a chip whose IGDB rows name those markets badges and
// seeds them first-class. Extension table: one row per region.
export const AVAILABILITY_REGIONS: Record<string, EntryRegion> = {
  japan: 'ntsc_j', asia: 'ntsc_j', north_america: 'ntsc_u',
  europe: 'pal', australia: 'pal', new_zealand: 'pal',
  korea: 'korea', brazil: 'brazil', china: 'china',
}

export type EntryRegion = 'ntsc_u' | 'ntsc_j' | 'pal' | 'korea' | 'brazil' | 'china'

// The TV-standard trio in REGIONS order (lib/listParams): the
// deterministic fill order when a worldwide row expands below. A
// worldwide release means the classic global markets - it never
// implies a language-market (korea/brazil/china) release, so the
// graduated three are deliberately not in this list.
const ENTRY_REGION_ORDER: EntryRegion[] = ['ntsc_u', 'ntsc_j', 'pal']

// platformEntryRegions turns a game platform's release_regions
// (canonical IGDB names, earliest-release-first off the wire) into the
// entry regions a copy on that platform can be - the chip's region
// list and the wizard's platform-first choice set. Unlike the badge
// era there is no suppression: a picker must show the full set, so a
// worldwide row expands to the TV-standard trio (concrete rows keep
// wire order, the expansion fills the remainder in entry-enum order).
export function platformEntryRegions(releaseRegions: string[] | undefined): EntryRegion[] {
  if (!releaseRegions) return []
  const regions: EntryRegion[] = []
  for (const region of releaseRegions) {
    const mapped = AVAILABILITY_REGIONS[region]
    if (mapped && !regions.includes(mapped)) regions.push(mapped)
  }
  if (releaseRegions.includes('worldwide')) {
    for (const r of ENTRY_REGION_ORDER) if (!regions.includes(r)) regions.push(r)
  }
  return regions
}

// LOCALE_HOME_REGIONS maps a UI locale to the entry region its users
// most likely hold copies from - the wizard's no-better-signal default.
// It never outranks a matched region: it only breaks ties when a
// canonical-name search leaves multiple possible regions on the picked
// chip (a JP-first worldwide release would otherwise always default
// NTSC-J via earliest-release order). Extend this table when a new UI
// locale ships; an unmapped locale contributes no home region.
const LOCALE_HOME_REGIONS: Record<string, EntryRegion> = {
  en: 'ntsc_u',
  ja: 'ntsc_j',
}

export function homeRegionFor(locale: string): EntryRegion | undefined {
  return LOCALE_HOME_REGIONS[locale]
}

// LOCALIZATION_CHAINS maps an entry region to its ordered localization
// identifiers - the frontend twin of the collection service's
// localizationChains, used to derive the wizard heading from the
// selected region. Extension table: one row per entry region that has
// localized bundles. china prefers Simplified script and falls back
// to Traditional when only a zh-TW bundle exists.
export const LOCALIZATION_CHAINS: Record<string, string[]> = {
  ntsc_j: ['ja-JP'],
  pal: ['EU'],
  korea: ['ko-KR'],
  china: ['zh-CN', 'zh-TW'],
  brazil: ['pt-BR'],
}

// REGION_PLATFORMS defaults a custom item's region from a
// region-specific platform pick (IGDB platform id -> entry region).
// Seeded with the JP-market trio; ids verified against the live
// /api/platforms catalog. A missing row costs only a missing default.
// Extension table: one row per region-locked platform.
export const REGION_PLATFORMS: Record<number, EntryRegion> = {
  99: 'ntsc_j', // Family Computer
  58: 'ntsc_j', // Super Famicom
  51: 'ntsc_j', // Famicom Disk System
}

// JP_CONSOLE_NAMES are PriceCharting's distinct-name JP market
// consoles - the ones filed without a "JP " prefix. Sibling of the
// server-side tables (the enrichment match gate, the collection class
// guard); a stale row here costs an incorrect NTSC-U tag and a
// user-correctable wizard default, never a wrong price.
const JP_CONSOLE_NAMES = new Set(['famicom', 'super famicom', 'famicom disk system'])

// consoleRegionFor derives the entry region a PriceCharting listing
// prices from its console-name axis: "PAL " prefix, "JP " prefix or a
// distinct JP market name, else the NA base catalog. Hardware and
// pc_listing rows show it as their region tag, and a hardware pick
// seeds the wizard's region default with it - a listing prices
// exactly one region, so any row that carries its console axis gets a
// tag; rows without a console name get none.
export function consoleRegionFor(consoleName: string): 'ntsc_u' | 'ntsc_j' | 'pal' {
  const c = consoleName.trim().toLowerCase()
  if (c.startsWith('pal ')) return 'pal'
  if (c.startsWith('jp ') || JP_CONSOLE_NAMES.has(c)) return 'ntsc_j'
  return 'ntsc_u'
}

// REGION_CLASS collapses an entry region to the class consoleRegionFor
// can actually distinguish: region_free carries no console prefix of
// its own, so it reads as the same class as ntsc_u (a listing prices
// exactly one of the three consoleRegionFor classes - never "free").
// korea, brazil and china also class base - PriceCharting has no axes
// for those markets, so the base catalog is their deliberate pricing
// proxy, and a JP or PAL listing on such an entry flags as a mismatch
// (mirrors the collection service's regionClass).
const REGION_CLASS: Record<string, 'base' | 'jp' | 'pal'> = {
  ntsc_u: 'base',
  region_free: 'base',
  korea: 'base',
  brazil: 'base',
  china: 'base',
  ntsc_j: 'jp',
  pal: 'pal',
}

// True when the listing's console region class and the entry's
// region class disagree - the standing state after a region change
// keeps a hand-picked match. Unknown entry regions never flag
// (open-world, same posture as the server's class rule).
export function regionMismatch(consoleName: string, region: string): boolean {
  const entryClass = REGION_CLASS[region]
  if (!entryClass) return false
  return REGION_CLASS[consoleRegionFor(consoleName)] !== entryClass
}

// LocalizationBundle is the structural subset of a search result's
// localizations this module reads (same reasoning as LocalizedTitled
// above: neither shape imports the other's generated type).
export interface LocalizationBundle {
  region: string
  name?: string
  translit?: string
}

export interface RegionTitle {
  text: string
  lang?: string
}

function nonEmpty(s: string | undefined): string | undefined {
  return s !== undefined && s !== '' ? s : undefined
}

// regionTitle resolves the product identity for a selected entry
// region: the first chain identifier with a bundle supplies the name,
// picked by the same form precedence entryTitle uses (the translit
// form never renders native script - it falls to the canonical name),
// with the same lang rules as entryTitleLang (-Latn when the rendered
// text is the transliteration). No usable bundle falls back to the
// canonical name with no lang claim.
export function regionTitle(
  canonicalName: string,
  bundles: LocalizationBundle[] | undefined,
  region: string,
  form: TitleForm,
): RegionTitle {
  for (const identifier of LOCALIZATION_CHAINS[region] ?? []) {
    const bundle = bundles?.find((b) => b.region === identifier)
    if (!bundle) continue
    const native = nonEmpty(bundle.name)
    const translit = nonEmpty(bundle.translit)
    const text = form === 'translit' ? translit : (native ?? translit)
    if (text === undefined) continue
    const subtag = bundleLang(identifier)
    const lang = subtag === undefined ? undefined : text === translit && translit !== native ? `${subtag}-Latn` : subtag
    return { text, lang }
  }
  return { text: canonicalName }
}
