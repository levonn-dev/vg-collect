import { cleanNames } from './credits'

// Pins every branch: trim, blank-drop, internal whitespace preserved,
// order preserved, empty-result collapse to undefined.
it('trims leading and trailing whitespace off every entry', () => {
  expect(cleanNames([' Nintendo ', '\tSega\n', 'Capcom'])).toEqual(['Nintendo', 'Sega', 'Capcom'])
})

it('drops entries that are empty or turn empty after trimming', () => {
  expect(cleanNames(['Nintendo', '', '   ', 'Sega'])).toEqual(['Nintendo', 'Sega'])
})

it('leaves internal whitespace alone - only the ends get trimmed', () => {
  expect(cleanNames(['Bandai  Namco', 'Square Enix'])).toEqual(['Bandai  Namco', 'Square Enix'])
})

it('keeps the surviving entries in their original order', () => {
  expect(cleanNames(['  Sega  ', '', 'Nintendo', '  ', 'Capcom'])).toEqual(['Sega', 'Nintendo', 'Capcom'])
})

it('collapses an empty array to undefined, not []', () => {
  expect(cleanNames([])).toBeUndefined()
})

it('collapses an all-blank input to undefined once every entry drops out', () => {
  expect(cleanNames(['', '   ', '\t\n'])).toBeUndefined()
})
