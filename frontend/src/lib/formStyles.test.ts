import { inputClass, labelClass, linkButtonClass } from './formStyles'

// Locks the byte-exact strings every converted site now imports
// instead of retyping - a change here is a deliberate style change
// across every one of them, not an accident in one file.
it('exposes the shared field styles verbatim', () => {
  expect(inputClass).toBe('rounded border border-gray-300 px-2 py-1 text-sm')
  expect(labelClass).toBe('flex flex-col gap-1 text-sm font-medium')
  expect(linkButtonClass).toBe('self-start text-xs text-gray-500 underline')
})
