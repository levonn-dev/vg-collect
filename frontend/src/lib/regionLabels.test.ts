import { i18n } from '@lingui/core'
import { messages } from '../locales/en.po'
import { regionLabelText } from './regionLabels'

i18n.load('en', messages)
i18n.activate('en')

test('known regions render their labels', () => {
  expect(regionLabelText(i18n, 'ntsc_j')).toBe('NTSC-J')
  expect(regionLabelText(i18n, 'region_free')).toBe('Region free')
  expect(regionLabelText(i18n, 'korea')).toBe('Korea')
  expect(regionLabelText(i18n, 'brazil')).toBe('Brazil')
  expect(regionLabelText(i18n, 'china')).toBe('China')
})

test('unknown regions render verbatim', () => {
  expect(regionLabelText(i18n, 'Taiwan')).toBe('Taiwan')
})
