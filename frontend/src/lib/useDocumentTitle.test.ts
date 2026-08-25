import { renderHook } from '@testing-library/react'
import { useDocumentTitle } from './useDocumentTitle'

it('sets document.title with the vgkeep suffix while mounted', () => {
  document.title = 'vgkeep'
  renderHook(() => useDocumentTitle('Collection'))
  expect(document.title).toBe('Collection - vgkeep')
})

it('updates the title when the input changes', () => {
  const { rerender } = renderHook(({ title }) => useDocumentTitle(title), {
    initialProps: { title: 'Collection' },
  })
  expect(document.title).toBe('Collection - vgkeep')
  rerender({ title: 'Explore' })
  expect(document.title).toBe('Explore - vgkeep')
})

it('restores the prior title on unmount', () => {
  document.title = 'vgkeep'
  const { unmount } = renderHook(() => useDocumentTitle('Account'))
  expect(document.title).toBe('Account - vgkeep')
  unmount()
  expect(document.title).toBe('vgkeep')
})
