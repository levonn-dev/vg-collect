import { i18n } from '@lingui/core'
import type { MessageDescriptor } from '@lingui/core'
import { messages } from '../locales/en.po'
import {
  conditionLabels,
  itemTypeWireLabels,
  packagingChipLabels,
  packagingWireLabels,
  statusLabels,
  statusWireLabels,
  visibilityLabels,
  visibilityWireLabels,
} from './enumLabels'
import { visibilityValues } from '../api/schema'
import { CONDITIONS, ITEM_TYPES, PACKAGINGS, STATUSES } from './listParams'

const VISIBILITY_VALUES = visibilityValues

i18n.load('en', messages)
i18n.activate('en')

// expectComplete is also the compile-time completeness check: its
// parameter type only accepts a Record<E, MessageDescriptor> for the
// same E as members, so a map missing an enum member - or a map whose
// own declared type ever loosens past Record<Enum, MessageDescriptor>
// - fails to satisfy this call. The runtime comparison below catches
// the same gap even for someone skimming test output without a
// type-checker.
function expectComplete<E extends string>(map: Record<E, MessageDescriptor>, members: readonly E[]): void {
  expect(Object.keys(map).sort()).toEqual([...members].sort())
}

test('every map covers exactly its enum, no more, no less', () => {
  expectComplete(statusLabels, STATUSES)
  expectComplete(statusWireLabels, STATUSES)
  expectComplete(conditionLabels, CONDITIONS)
  expectComplete(packagingWireLabels, PACKAGINGS)
  expectComplete(packagingChipLabels, PACKAGINGS)
  expectComplete(itemTypeWireLabels, ITEM_TYPES)
  expectComplete(visibilityLabels, VISIBILITY_VALUES)
  expectComplete(visibilityWireLabels, VISIBILITY_VALUES)
})

test('known labels resolve to their expected text under the test i18n', () => {
  expect(i18n._(statusLabels.backlog)).toBe('Backlog')
  expect(i18n._(statusWireLabels.backlog)).toBe('backlog')
  expect(i18n._(conditionLabels.near_mint)).toBe('Near mint')
  expect(i18n._(packagingWireLabels.cib)).toBe('cib')
  expect(i18n._(packagingChipLabels.cib)).toBe('CIB')
  expect(i18n._(itemTypeWireLabels.console)).toBe('console')
  expect(i18n._(visibilityLabels.unlisted)).toBe('Unlisted')
  expect(i18n._(visibilityWireLabels.unlisted)).toBe('unlisted')
})

test('the Title-case and wire-case forms stay genuinely distinct strings', () => {
  expect(i18n._(statusLabels.playing)).not.toBe(i18n._(statusWireLabels.playing))
  expect(i18n._(packagingChipLabels.sealed)).not.toBe(i18n._(packagingWireLabels.sealed))
  expect(i18n._(visibilityLabels.listed)).not.toBe(i18n._(visibilityWireLabels.listed))
})
