import { btnPrimary, btnSecondary, btnSecondaryXs, inputClass, labelClass, linkButtonClass } from './formStyles'

// Locks byte-exact strings every converted site imports; a change here
// is deliberate across all of them.
it('exposes the shared field styles verbatim', () => {
  expect(inputClass).toBe('rounded border border-gray-300 px-2 py-1 text-sm')
  expect(labelClass).toBe('flex flex-col gap-1 text-sm font-medium')
  expect(linkButtonClass).toBe('self-start text-xs text-gray-500 underline')
})

it('exposes the shared button styles verbatim', () => {
  expect(btnSecondary).toBe('rounded border border-gray-300 px-3 py-1 text-sm hover:bg-gray-50 disabled:opacity-50')
  expect(btnSecondaryXs).toBe('rounded border border-gray-300 px-2 py-0.5 text-xs hover:bg-gray-50 disabled:opacity-50')
  expect(btnPrimary).toBe('rounded bg-gray-900 px-4 py-2 text-center text-sm font-medium text-white hover:bg-gray-700 disabled:opacity-50')
})
