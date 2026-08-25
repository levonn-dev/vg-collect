// Shared Tailwind classes so fields render pixel-identical across the
// wizard, entry form, and catalog pickers; one edit point.
export const inputClass = 'rounded border border-gray-300 px-2 py-1 text-sm'
export const labelClass = 'flex flex-col gap-1 text-sm font-medium'
export const linkButtonClass = 'self-start text-xs text-gray-500 underline'

// Same idiom for buttons; layout utilities that vary by call site
// (w-full, mt-*, ...) stay out, composed at the call site.
export const btnSecondary = 'rounded border border-gray-300 px-3 py-1 text-sm hover:bg-gray-50 disabled:opacity-50'
export const btnSecondaryXs = 'rounded border border-gray-300 px-2 py-0.5 text-xs hover:bg-gray-50 disabled:opacity-50'
export const btnPrimary = 'rounded bg-gray-900 px-4 py-2 text-center text-sm font-medium text-white hover:bg-gray-700 disabled:opacity-50'
