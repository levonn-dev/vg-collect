import { fireEvent, render, screen } from '@testing-library/react'
import Avatar from './Avatar'

it('renders the provider image without a referrer when a url is present', () => {
  render(<Avatar url="https://img.example/a.png" label="Alice" size="md" />)
  const img = document.querySelector('img')
  expect(img).toHaveAttribute('src', 'https://img.example/a.png')
  expect(img).toHaveAttribute('referrerpolicy', 'no-referrer')
  expect(img).toHaveAttribute('alt', '')
})

it('falls back to the initial when there is no url, sized per the size prop', () => {
  render(<Avatar label="Alice" size="lg" />)
  expect(document.querySelector('img')).toBeNull()
  const fallback = screen.getByText('A', { selector: 'span[aria-hidden="true"]' })
  expect(fallback).toBeInTheDocument()
  expect(fallback).toHaveClass('h-16', 'w-16', 'text-2xl')
})

it('falls back to the initial once the image fails to load', () => {
  render(<Avatar url="https://img.example/a.png" label="Alice" size="md" />)
  fireEvent.error(document.querySelector('img')!)
  expect(document.querySelector('img')).toBeNull()
  expect(screen.getByText('A', { selector: 'span[aria-hidden="true"]' })).toBeInTheDocument()
})

it('remounts and retries the image when a url-keyed caller changes the url', () => {
  function Wrapper({ url }: { url: string }) {
    return <Avatar key={url} url={url} label="Alice" size="md" />
  }
  const { rerender } = render(<Wrapper url="https://img.example/a.png" />)
  fireEvent.error(document.querySelector('img')!)
  expect(document.querySelector('img')).toBeNull() // fell back to the initial

  rerender(<Wrapper url="https://img.example/b.png" />)
  // The changed key remounts a fresh instance: failed resets to
  // false, so the new url gets its own attempt instead of staying
  // stuck on the old url's failure.
  const img = document.querySelector('img')
  expect(img).toHaveAttribute('src', 'https://img.example/b.png')
})
