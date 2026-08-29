import { renderHook } from '@testing-library/react'
import { useKonami } from './useKonami'

const SEQUENCE = [
  'ArrowUp', 'ArrowUp', 'ArrowDown', 'ArrowDown',
  'ArrowLeft', 'ArrowRight', 'ArrowLeft', 'ArrowRight',
  'b', 'a',
]

function press(key: string, target: EventTarget = window) {
  target.dispatchEvent(new KeyboardEvent('keydown', { key, bubbles: true }))
}

it('fires the callback each time the full sequence is entered', () => {
  const onCode = vi.fn()
  renderHook(() => useKonami(onCode))
  SEQUENCE.forEach((key) => press(key))
  expect(onCode).toHaveBeenCalledTimes(1)
  SEQUENCE.forEach((key) => press(key))
  expect(onCode).toHaveBeenCalledTimes(2)
})

it('a wrong key resets progress', () => {
  const onCode = vi.fn()
  renderHook(() => useKonami(onCode))
  press('ArrowUp')
  press('ArrowUp')
  press('x')
  SEQUENCE.slice(2).forEach((key) => press(key))
  expect(onCode).not.toHaveBeenCalled()
  SEQUENCE.forEach((key) => press(key))
  expect(onCode).toHaveBeenCalledTimes(1)
})

it('an extra ArrowUp restarts the sequence rather than killing it', () => {
  const onCode = vi.fn()
  renderHook(() => useKonami(onCode))
  press('ArrowUp')
  press('ArrowUp')
  press('ArrowUp')
  SEQUENCE.slice(1).forEach((key) => press(key))
  expect(onCode).toHaveBeenCalledTimes(1)
})

it('matches letters case-insensitively', () => {
  const onCode = vi.fn()
  renderHook(() => useKonami(onCode))
  SEQUENCE.slice(0, 8).forEach((key) => press(key))
  press('B')
  press('A')
  expect(onCode).toHaveBeenCalledTimes(1)
})

it('ignores keystrokes inside editable elements', () => {
  const onCode = vi.fn()
  renderHook(() => useKonami(onCode))
  const input = document.createElement('input')
  document.body.appendChild(input)
  SEQUENCE.forEach((key) => press(key, input))
  expect(onCode).not.toHaveBeenCalled()
  input.remove()
})

it('stops listening on unmount', () => {
  const onCode = vi.fn()
  const { unmount } = renderHook(() => useKonami(onCode))
  unmount()
  SEQUENCE.forEach((key) => press(key))
  expect(onCode).not.toHaveBeenCalled()
})
