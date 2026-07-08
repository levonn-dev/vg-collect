import { render } from '@testing-library/react'
import ItemTypeIcon from './ItemTypeIcon'

it.each([
  ['game', 'game'],
  ['console', 'console'],
  ['accessory', 'accessory'],
])('renders the %s glyph', (type, expected) => {
  const { container } = render(<ItemTypeIcon type={type} />)
  expect(container.querySelector(`svg[data-icon="${expected}"]`)).toBeInTheDocument()
})

it('falls back to the game glyph for unknown or missing types', () => {
  const { container } = render(<ItemTypeIcon type="amiibo" />)
  expect(container.querySelector('svg[data-icon="game"]')).toBeInTheDocument()
  const { container: bare } = render(<ItemTypeIcon />)
  expect(bare.querySelector('svg[data-icon="game"]')).toBeInTheDocument()
})

it('stays decorative', () => {
  const { container } = render(<ItemTypeIcon type="console" />)
  expect(container.querySelector('svg')).toHaveAttribute('aria-hidden', 'true')
})
