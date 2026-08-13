import { screen } from '@testing-library/react'
import { renderWithI18n } from '../test/i18n'
import PriceTriple from './PriceTriple'

it('renders the three prices in order', () => {
  renderWithI18n(<PriceTriple loose="$11.00" cib="$17.60" newPrice="$30.25" className="text-xs text-gray-500" />)
  expect(screen.getByText('Loose $11.00 / CIB $17.60 / New $30.25')).toBeInTheDocument()
})

it('renders a missing price as the caller-supplied placeholder verbatim', () => {
  renderWithI18n(<PriceTriple loose="$8.00" cib="-" newPrice="$20.00" className="text-xs text-gray-500" />)
  expect(screen.getByText('Loose $8.00 / CIB - / New $20.00')).toBeInTheDocument()
})

it('applies the caller-supplied className to the wrapping paragraph', () => {
  renderWithI18n(<PriceTriple loose="-" cib="-" newPrice="-" className="mt-1 text-xs text-green-800" />)
  expect(screen.getByText('Loose - / CIB - / New -').className).toBe('mt-1 text-xs text-green-800')
})
