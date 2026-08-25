import { i18n } from '@lingui/core'
import { render } from '@testing-library/react'
import { entryFixture, sharedEntryFixture } from '../../test/fixtures'
import type { Entry } from '../../api/collection'
import type { DisplayMoney } from '../../lib/useDisplayMoney'
import { rowMeta } from './rowMeta'

// rowMeta only ever calls money.entryValue; the rest exists to satisfy the
// interface for this direct, no-render unit test.
function moneyStub(entryValue: (e: Entry) => string | null): DisplayMoney {
  return {
    currency: 'USD',
    profileCurrency: 'USD',
    ready: true,
    rateStale: false,
    rateFor: () => 1,
    format: () => null,
    format0: () => null,
    entryValue,
  }
}

function starText(pin: unknown): string {
  const { container } = render(<>{pin}</>)
  return container.textContent ?? ''
}

it('delegates the pin slot to the caller-supplied renderer for a full (own) entry', () => {
  const entry = entryFixture({ pinned: true })
  const pinSlot = vi.fn((e: Entry) => <span data-testid="custom-pin">{e.id}</span>)
  const meta = rowMeta(entry, moneyStub(() => null), i18n, { pinSlot })
  expect(pinSlot).toHaveBeenCalledWith(entry)
  const { getByTestId } = render(<>{meta.pin}</>)
  expect(getByTestId('custom-pin')).toHaveTextContent(entry.id)
})

it('never calls pinSlot for a SharedEntry (not a full Entry), even when the caller passes one', () => {
  const entry = sharedEntryFixture({ pinned: true })
  const pinSlot = vi.fn()
  const meta = rowMeta(entry, moneyStub(() => null), i18n, { pinSlot })
  expect(pinSlot).not.toHaveBeenCalled()
  expect(starText(meta.pin)).toBe('★') // falls back to the static star instead
})

it('falls back to a static star with no trailing space by default when pinned and no pinSlot', () => {
  const entry = entryFixture({ pinned: true })
  const meta = rowMeta(entry, moneyStub(() => null), i18n)
  expect(starText(meta.pin)).toBe('★')
})

it('adds the trailing space only when pinTrailingSpace is set (EntryTable\'s plain-text flow)', () => {
  const entry = entryFixture({ pinned: true })
  const meta = rowMeta(entry, moneyStub(() => null), i18n, { pinTrailingSpace: true })
  expect(starText(meta.pin)).toBe('★ ')
})

it('renders no pin at all when the entry is not pinned', () => {
  const entry = entryFixture({ pinned: false })
  const meta = rowMeta(entry, moneyStub(() => null), i18n)
  expect(meta.pin).toBeFalsy()
})

it('platform falls back to a dash when the entry carries none', () => {
  const withPlatform = rowMeta(entryFixture({ platform: { name: 'SNES' } }), moneyStub(() => null), i18n)
  expect(withPlatform.platform).toBe('SNES')
  const withoutPlatform = rowMeta(entryFixture({ platform: undefined }), moneyStub(() => null), i18n)
  expect(withoutPlatform.platform).toBe('-')
})

it('value reads money.entryValue for a full entry, falling back to a dash when it has none', () => {
  const entry = entryFixture()
  const priced = rowMeta(entry, moneyStub(() => '$42.00'), i18n)
  expect(priced.value).toBe('$42.00')
  const unpriced = rowMeta(entry, moneyStub(() => null), i18n)
  expect(unpriced.value).toBe('-')
})

it('value is always a dash for a SharedEntry, regardless of money', () => {
  const entry = sharedEntryFixture()
  const meta = rowMeta(entry, moneyStub(() => '$42.00'), i18n)
  expect(meta.value).toBe('-')
})
