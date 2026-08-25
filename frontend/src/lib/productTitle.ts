// Display-selection helpers for region-localized product titles.
// No React, no Lingui: only picks which wire field to render.

import { AVAILABILITY_REGIONS, LOCALIZATION_CHAINS, REGION_CLASS, REGION_PLATFORMS, consoleRegionFor } from '../gen/domain'

export type TitleForm = 'translit' | 'native'

// Default title form per locale: translit (romanized) or native
// (original script).
const LOCALE_TITLE_FORMS: Record<string, TitleForm> = {
  en: 'translit',
  ja: 'native',
}

export function titleFormFor(locale: string): TitleForm {
  return LOCALE_TITLE_FORMS[locale] ?? 'translit'
}

// Structural subset of entry/search-result fields this module reads,
// not imported from the generated schema, so it fits either shape.
export interface LocalizedTitled {
  display_name: string
  localized_name?: string
  localized_name_translit?: string
  localized_cover_url?: string
  cover_url?: string
  region?: string
}

// translit form skips native script entirely so a Latin locale is
// never fronted by CJK; native form falls through translit before canonical.
export function entryTitle(e: LocalizedTitled, form: TitleForm): string {
  if (form === 'translit') return e.localized_name_translit ?? e.display_name
  return e.localized_name ?? e.localized_name_translit ?? e.display_name
}

// Secondary line under the title; omitted when it would duplicate entryTitle.
export function entrySecondary(e: LocalizedTitled, form: TitleForm): string | undefined {
  const title = entryTitle(e, form)
  if (title !== e.display_name) return e.display_name
  return e.localized_name !== undefined && e.localized_name !== title ? e.localized_name : undefined
}

// Lang tag for the secondary line; unset when that line is the canonical name.
export function entrySecondaryLang(e: LocalizedTitled, form: TitleForm): string | undefined {
  const secondary = entrySecondary(e, form)
  if (secondary === undefined || secondary === e.display_name) return undefined
  return e.region ? REGION_LANGS[e.region] : undefined
}

// Prefers region-localized box art over the entry's own cover_url.
export function entryCover(e: LocalizedTitled): string | undefined {
  return e.localized_cover_url ?? e.cover_url
}

// Entry region -> BCP-47 subtag; ntsc_u/pal/region_free have none (pal
// is cover-only). china stays bare zh: its chain can resolve either script.
export const REGION_LANGS: Record<string, string> = {
  ntsc_j: 'ja',
  korea: 'ko',
  china: 'zh',
  brazil: 'pt',
}

// Which field supplied entryTitle's value, for entryTitleLang's lang-tag decision.
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

// Lang tag for entryTitle's actual text (tracks the field picked, not
// the requested form); -Latn suffix marks a transliteration.
export function entryTitleLang(e: LocalizedTitled, form: TitleForm): string | undefined {
  const source = titleSource(e, form)
  if (source === 'canonical') return undefined
  const subtag = e.region ? REGION_LANGS[e.region] : undefined
  if (!subtag) return undefined
  return source === 'translit' ? `${subtag}-Latn` : subtag
}

// Leading subtag of a locale-form region id ("ja-JP" -> "ja"); continent
// ids ("EU") match nothing.
const LOCALE_FORM_LANG = /^[a-z]{2,3}(?=-)/

// Language subtag from a bundle's free-form region id (matched_region),
// vs REGION_LANGS which maps the entry region enum.
export function bundleLang(regionIdentifier: string): string | undefined {
  return LOCALE_FORM_LANG.exec(regionIdentifier)?.[0]
}

// matched_region -> default entry region, only where unambiguous
// (ko-KR today has none; wizard keeps its own default).
export const REGION_FROM_MATCH: Record<string, 'ntsc_u' | 'ntsc_j' | 'pal' | 'region_free'> = {
  'ja-JP': 'ntsc_j',
  EU: 'pal',
}

export type EntryRegion = 'ntsc_u' | 'ntsc_j' | 'pal' | 'korea' | 'brazil' | 'china'

// Fill order when a worldwide row expands (never implies korea/brazil/
// china). Hand-coded, not generated: domain.yaml gives worldwide no
// entry_region since it fans out to all three, not one.
const ENTRY_REGION_ORDER: EntryRegion[] = ['ntsc_u', 'ntsc_j', 'pal']

// release_regions (earliest-release-first) -> entry regions; worldwide
// expands to the TV-standard trio, concrete rows keep wire order.
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

// Locale's likely home region; a tie-breaker only, never overrides a
// matched region (else JP-first worldwide would always win NTSC-J).
const LOCALE_HOME_REGIONS: Record<string, EntryRegion> = {
  en: 'ntsc_u',
  ja: 'ntsc_j',
}

export function homeRegionFor(locale: string): EntryRegion | undefined {
  return LOCALE_HOME_REGIONS[locale]
}

// Generated from api/domain.yaml (task gen). AVAILABILITY_REGIONS's Go
// twin, regionkit.ReleaseRegionNames, is pinned by an anti-drift test
// against the bff contract enum.
export { LOCALIZATION_CHAINS, REGION_PLATFORMS, consoleRegionFor }

// True when console and entry region class disagree; unknown entry
// regions never flag (matches the server's class rule).
export function regionMismatch(consoleName: string, region: string): boolean {
  const entryClass = REGION_CLASS[region]
  if (!entryClass) return false
  return REGION_CLASS[consoleRegionFor(consoleName)] !== entryClass
}

// Structural subset of a search result's localizations (same reasoning
// as LocalizedTitled).
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

// First chain identifier with a bundle wins, using entryTitle's form
// precedence and entryTitleLang's lang rules; no bundle keeps the
// canonical name with no lang.
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
