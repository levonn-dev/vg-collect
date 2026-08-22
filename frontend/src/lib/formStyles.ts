// Shared Tailwind classes for the plain form fields across the wizard
// steps, the entry form, and the catalog pickers: one definition per
// combo so every <input>/<select>/<textarea>/<label> across those
// files renders pixel-identical, and a deliberate style change only
// touches this file instead of five.
export const inputClass = 'rounded border border-gray-300 px-2 py-1 text-sm'
export const labelClass = 'flex flex-col gap-1 text-sm font-medium'
export const linkButtonClass = 'self-start text-xs text-gray-500 underline'

// Shared button classes, same idiom as the field styles above: the
// bordered secondary button (two sizes) and the filled primary button
// used across pages/components/wizard steps. Layout utilities that
// vary by call site (flex-1, w-full, mt-*, self-start, ...) stay out
// of these constants and get composed on at the call site instead,
// e.g. `${btnSecondary} w-full`.
export const btnSecondary = 'rounded border border-gray-300 px-3 py-1 text-sm hover:bg-gray-50 disabled:opacity-50'
export const btnSecondaryXs = 'rounded border border-gray-300 px-2 py-0.5 text-xs hover:bg-gray-50 disabled:opacity-50'
export const btnPrimary = 'rounded bg-gray-900 px-4 py-2 text-center text-sm font-medium text-white hover:bg-gray-700 disabled:opacity-50'
