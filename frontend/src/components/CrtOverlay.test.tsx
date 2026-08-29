import { act, render } from '@testing-library/react'
import CrtOverlay from './CrtOverlay'

const SEQUENCE = [
  'ArrowUp', 'ArrowUp', 'ArrowDown', 'ArrowDown',
  'ArrowLeft', 'ArrowRight', 'ArrowLeft', 'ArrowRight',
  'b', 'a',
]

function enterCode() {
  act(() => {
    SEQUENCE.forEach((key) =>
      window.dispatchEvent(new KeyboardEvent('keydown', { key, bubbles: true })),
    )
  })
}

function overlay(container: HTMLElement) {
  return container.querySelector('[data-testid="crt-overlay"]')
}

it('renders nothing until the code is entered', () => {
  const { container } = render(<CrtOverlay />)
  expect(overlay(container)).toBeNull()
})

it('the code toggles the overlay on, hidden from the a11y tree', () => {
  const { container } = render(<CrtOverlay />)
  enterCode()
  const el = overlay(container)
  expect(el).not.toBeNull()
  expect(el).toHaveAttribute('aria-hidden', 'true')
})

it('entering the code again toggles the overlay off', () => {
  const { container } = render(<CrtOverlay />)
  enterCode()
  expect(overlay(container)).not.toBeNull()
  enterCode()
  expect(overlay(container)).toBeNull()
})
