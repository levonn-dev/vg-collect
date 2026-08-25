import { bundleLang, consoleRegionFor, entryCover, entrySecondary, entrySecondaryLang, entryTitle, entryTitleLang, REGION_LANGS, titleFormFor, homeRegionFor, LOCALIZATION_CHAINS, REGION_PLATFORMS, platformEntryRegions, regionMismatch, regionTitle } from './productTitle'

const jp = {
  display_name: 'Trials of Mana',
  localized_name: '聖剣伝説 3',
  localized_name_translit: 'Seiken Densetsu 3',
  localized_cover_url: 'https://x/jp.jpg',
  cover_url: 'https://x/na.jpg',
  region: 'ntsc_j',
}

it('translit form prefers romanization', () => {
  expect(entryTitle(jp, 'translit')).toBe('Seiken Densetsu 3')
})

it('native form prefers native script', () => {
  expect(entryTitle(jp, 'native')).toBe('聖剣伝説 3')
})

// translit never falls back to native script; canonical name shows
// instead, native moves to secondary.
it('missing translit falls back to canonical, never native script', () => {
  expect(entryTitle({ ...jp, localized_name_translit: undefined }, 'translit')).toBe('Trials of Mana')
  expect(entryTitle({ display_name: 'Trials of Mana' }, 'translit')).toBe('Trials of Mana')
})

// native form falls to translit before canonical: translit is still
// the localized identity.
it('missing native falls back to translit then canonical', () => {
  expect(entryTitle({ ...jp, localized_name: undefined }, 'native')).toBe('Seiken Densetsu 3')
  expect(entryTitle({ display_name: 'Trials of Mana' }, 'native')).toBe('Trials of Mana')
})

it('secondary is canonical only when it differs', () => {
  expect(entrySecondary(jp, 'native')).toBe('Trials of Mana')
  expect(entrySecondary({ display_name: 'Trials of Mana' }, 'native')).toBeUndefined()
})

it('secondary surfaces the native title the translit form skipped', () => {
  const kr = { display_name: 'Trials of Mana', localized_name: '성검전설 3', region: 'korea' }
  expect(entryTitle(kr, 'translit')).toBe('Trials of Mana')
  expect(entrySecondary(kr, 'translit')).toBe('성검전설 3')
  expect(entrySecondaryLang(kr, 'translit')).toBe('ko')
  // A canonical secondary carries no lang tag.
  expect(entrySecondaryLang(jp, 'native')).toBeUndefined()
})

it('cover prefers the localized art', () => {
  expect(entryCover(jp)).toBe('https://x/jp.jpg')
  expect(entryCover({ ...jp, localized_cover_url: undefined })).toBe('https://x/na.jpg')
})

it('lang rides the chosen form', () => {
  expect(entryTitleLang(jp, 'native')).toBe('ja')
  expect(entryTitleLang(jp, 'translit')).toBe('ja-Latn')
  expect(entryTitleLang({ display_name: 'x' }, 'native')).toBeUndefined()
})

// Lang follows what actually renders, not the requested form:
// translit->canonical has no tag, native->translit keeps -Latn.
it('lang follows the field actually chosen, not the requested form', () => {
  expect(entryTitleLang({ ...jp, localized_name_translit: undefined }, 'translit')).toBeUndefined()
  expect(entryTitleLang({ ...jp, localized_name: undefined }, 'native')).toBe('ja-Latn')
})

// No region, or a region REGION_LANGS doesn't map: both are
// undefined, not just the unset case.
it('lang is undefined when the region has no mapping, even with a localized title', () => {
  expect(entryTitleLang({ display_name: 'Trials of Mana', localized_name: '聖剣伝説 3' }, 'native')).toBeUndefined()
  expect(entryTitleLang({ display_name: 'Trials of Mana', localized_name: '聖剣伝説 3', region: 'pal' }, 'native')).toBeUndefined()
})

// A region mapping alone isn't enough: the text also has to be the localized field.
it('lang is undefined for a canonical title even when the region maps', () => {
  expect(entryTitleLang({ display_name: 'Trials of Mana', region: 'ntsc_j' }, 'native')).toBeUndefined()
})

it('titleFormFor', () => {
  expect(titleFormFor('en')).toBe('translit')
  expect(titleFormFor('ja')).toBe('native')
  expect(titleFormFor('ko')).toBe('translit')
})

it('bundleLang', () => {
  expect(bundleLang('ja-JP')).toBe('ja')
  expect(bundleLang('EU')).toBeUndefined()
})

it('REGION_LANGS maps each entry region to its BCP-47 language subtag', () => {
  expect(REGION_LANGS).toEqual({ ntsc_j: 'ja', korea: 'ko', china: 'zh', brazil: 'pt' })
})

it('platformEntryRegions: maps wire order and dedupes like the badge era', () => {
  expect(platformEntryRegions(['japan', 'europe'])).toEqual(['ntsc_j', 'pal'])
  expect(platformEntryRegions(['europe', 'australia'])).toEqual(['pal'])
  expect(platformEntryRegions(['korea', 'japan'])).toEqual(['korea', 'ntsc_j'])
})

it('platformEntryRegions: the graduated markets map to themselves', () => {
  expect(platformEntryRegions(['korea'])).toEqual(['korea'])
  expect(platformEntryRegions(['brazil', 'north_america'])).toEqual(['brazil', 'ntsc_u'])
  expect(platformEntryRegions(['china'])).toEqual(['china'])
})

it('platformEntryRegions: keeps the full all-three set instead of suppressing', () => {
  expect(platformEntryRegions(['japan', 'north_america', 'australia', 'europe']))
    .toEqual(['ntsc_j', 'ntsc_u', 'pal'])
})

it('platformEntryRegions: a pure worldwide row expands in entry-enum order', () => {
  expect(platformEntryRegions(['worldwide'])).toEqual(['ntsc_u', 'ntsc_j', 'pal'])
})

it('platformEntryRegions: concrete regions lead, worldwide fills the remainder', () => {
  expect(platformEntryRegions(['japan', 'worldwide'])).toEqual(['ntsc_j', 'ntsc_u', 'pal'])
})

it('platformEntryRegions: nothing mappable yields the empty set', () => {
  expect(platformEntryRegions(undefined)).toEqual([])
  expect(platformEntryRegions([])).toEqual([])
  expect(platformEntryRegions(['moon_base_region'])).toEqual([])
})

it('LOCALIZATION_CHAINS mirrors the collection service chains', () => {
  expect(LOCALIZATION_CHAINS).toEqual({
    ntsc_j: ['ja-JP'],
    pal: ['EU'],
    korea: ['ko-KR'],
    china: ['zh-CN', 'zh-TW'],
    brazil: ['pt-BR'],
  })
})

it('REGION_PLATFORMS pins the verified JP-market platform ids', () => {
  expect(REGION_PLATFORMS).toEqual({ 99: 'ntsc_j', 58: 'ntsc_j', 51: 'ntsc_j' })
})

// Chain lookup, then the same form precedence and -Latn rules
// entryTitleLang uses.
const bundles = [
  { region: 'ja-JP', name: '聖剣伝説 2', translit: 'Seiken Densetsu 2' },
  { region: 'EU' },
]

it('regionTitle: a name-only korea bundle falls back to canonical under the translit form', () => {
  const koBundles = [{ region: 'ko-KR', name: '성검전설 3' }]
  expect(regionTitle('Trials of Mana', koBundles, 'korea', 'translit'))
    .toEqual({ text: 'Trials of Mana' })
  expect(regionTitle('Trials of Mana', koBundles, 'korea', 'native'))
    .toEqual({ text: '성검전설 3', lang: 'ko' })
})

it('regionTitle: translit form picks the romanization with a -Latn tag', () => {
  expect(regionTitle('Secret of Mana', bundles, 'ntsc_j', 'translit'))
    .toEqual({ text: 'Seiken Densetsu 2', lang: 'ja-Latn' })
})

it('regionTitle: native form picks the native script with the bare tag', () => {
  expect(regionTitle('Secret of Mana', bundles, 'ntsc_j', 'native'))
    .toEqual({ text: '聖剣伝説 2', lang: 'ja' })
})

// native falls to translit (still localized, already Latin); translit
// never falls to native, goes canonical instead.
it('regionTitle: only the native form falls back across forms', () => {
  expect(regionTitle('X', [{ region: 'ja-JP', name: '聖剣伝説 2' }], 'ntsc_j', 'translit'))
    .toEqual({ text: 'X' })
  expect(regionTitle('X', [{ region: 'ja-JP', translit: 'Seiken' }], 'ntsc_j', 'native'))
    .toEqual({ text: 'Seiken', lang: 'ja-Latn' })
})

it('regionTitle: an empty or missing bundle falls back to the canonical name', () => {
  // The EU bundle above carries no name fields; pal must not blank the title.
  expect(regionTitle('Secret of Mana', bundles, 'pal', 'translit'))
    .toEqual({ text: 'Secret of Mana' })
  expect(regionTitle('Secret of Mana', bundles, 'ntsc_u', 'translit'))
    .toEqual({ text: 'Secret of Mana' })
  expect(regionTitle('Secret of Mana', undefined, 'ntsc_j', 'translit'))
    .toEqual({ text: 'Secret of Mana' })
})

it('regionTitle: a continent-form identifier yields text without a lang tag', () => {
  expect(regionTitle('Secret of Mana', [{ region: 'EU', translit: 'Secret of Mana EU' }], 'pal', 'translit'))
    .toEqual({ text: 'Secret of Mana EU' })
})

it('regionTitle: empty-string fields count as absent', () => {
  expect(regionTitle('X', [{ region: 'ja-JP', name: '', translit: '' }], 'ntsc_j', 'translit'))
    .toEqual({ text: 'X' })
})

it('homeRegionFor maps each UI locale to its home entry region', () => {
  expect(homeRegionFor('en')).toBe('ntsc_u')
  expect(homeRegionFor('ja')).toBe('ntsc_j')
  expect(homeRegionFor('ko')).toBeUndefined()
})

describe('consoleRegionFor', () => {
  it.each([
    ['Super Nintendo', 'ntsc_u'],
    ['Playstation 4', 'ntsc_u'],
    ['PAL Super Nintendo', 'pal'],
    ['PAL Xbox Series X', 'pal'],
    ['JP Sega Saturn', 'ntsc_j'],
    ['JP Nintendo Switch', 'ntsc_j'],
    ['Super Famicom', 'ntsc_j'],
    ['Famicom', 'ntsc_j'],
    ['Famicom Disk System', 'ntsc_j'],
    ['  Super Famicom  ', 'ntsc_j'],
    ['Palworld Collection', 'ntsc_u'], // "pal" without the space suffix is not the PAL prefix
    ['', 'ntsc_u'],
  ])('%s -> %s', (consoleName, region) => {
    expect(consoleRegionFor(consoleName)).toBe(region)
  })
})

describe('regionMismatch', () => {
  it.each([
    ['Super Famicom', 'ntsc_j', false],
    ['Super Famicom', 'ntsc_u', true],
    ['Super Nintendo', 'ntsc_j', true],
    ['Super Nintendo', 'ntsc_u', false],
    ['Super Nintendo', 'region_free', false],
    ['PAL Super Nintendo', 'pal', false],
    ['PAL Super Nintendo', 'ntsc_u', true],
    ['Super Nintendo', 'korea', false], // base is korea's pricing proxy (no PC axis)
    ['Super Nintendo', 'china', false], // same proxy rule
    ['Super Famicom', 'korea', true], // a JP listing still misprices a korea copy
    ['PAL Super Nintendo', 'brazil', true],
    ['Super Nintendo', 'someday_region', false],
  ])('%s vs %s -> %s', (consoleName, region, want) => {
    expect(regionMismatch(consoleName, region)).toBe(want)
  })
})
