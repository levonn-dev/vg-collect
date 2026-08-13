// Shared Tailwind classes for the plain form fields across the wizard
// steps, the entry form, and the catalog pickers: one definition per
// combo so every <input>/<select>/<textarea>/<label> across those
// files renders pixel-identical, and a deliberate style change only
// touches this file instead of five.
export const inputClass = 'rounded border border-gray-300 px-2 py-1 text-sm'
export const labelClass = 'flex flex-col gap-1 text-sm font-medium'
export const linkButtonClass = 'self-start text-xs text-gray-500 underline'
