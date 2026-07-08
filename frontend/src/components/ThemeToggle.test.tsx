import { act, render, screen } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import ThemeToggle from './ThemeToggle'

afterEach(() => {
  document.documentElement.classList.remove('dark')
  localStorage.clear()
})

it('flips the html class and persists the choice', async () => {
  document.documentElement.classList.add('dark')
  render(<ThemeToggle />)
  await userEvent.click(screen.getByRole('button', { name: 'Switch to light mode' }))
  expect(document.documentElement.classList.contains('dark')).toBe(false)
  expect(localStorage.getItem('theme')).toBe('light')
  await userEvent.click(screen.getByRole('button', { name: 'Switch to dark mode' }))
  expect(document.documentElement.classList.contains('dark')).toBe(true)
  expect(localStorage.getItem('theme')).toBe('dark')
})

it('follows live system changes only until the user chooses', async () => {
  let notify: (() => void) | undefined
  const mq = {
    matches: false, // system prefers dark
    addEventListener: (_: string, h: () => void) => {
      notify = h
    },
    removeEventListener: () => {},
  }
  Object.defineProperty(window, 'matchMedia', { writable: true, value: () => mq })
  document.documentElement.classList.add('dark')
  render(<ThemeToggle />)

  // No stored choice: a system flip to light is followed.
  mq.matches = true
  act(() => notify?.())
  expect(document.documentElement.classList.contains('dark')).toBe(false)

  // An explicit choice pins the theme against further system flips.
  await userEvent.click(screen.getByRole('button', { name: 'Switch to dark mode' }))
  mq.matches = false
  act(() => notify?.())
  expect(document.documentElement.classList.contains('dark')).toBe(true)
})
