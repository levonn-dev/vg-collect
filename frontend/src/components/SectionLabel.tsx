import type { ReactNode } from 'react'

type SectionLabelElement = 'h3' | 'p' | 'legend'
type SectionLabelSize = 'xs' | 'sm'

// Tailwind only picks up literal class strings; `text-${size}` would drop
// from the build the moment the value stops appearing verbatim elsewhere.
const SIZES: Record<SectionLabelSize, string> = {
  xs: 'text-xs',
  sm: 'text-sm',
}

interface SectionLabelProps {
  as: SectionLabelElement
  size: SectionLabelSize
  // Every site is font-semibold except Login/Account's "Dev fixtures" caption
  // (a lighter secondary hint, not a section opener).
  bold?: boolean
  // Only value that differs beyond size/weight/element: mb-1/mb-2/mb-3, or
  // FilterBar/BulkEditBar's float-left mr-2 (legend floats beside the
  // fieldset's first row). Prepended ahead of the shared classes.
  className?: string
  children: ReactNode
}

// Uppercase, tracking-wide caption shared by section headings, stat labels,
// and fieldset legends, varying only in size/weight/element/margin per site.
export default function SectionLabel({ as: Tag, size, bold = true, className, children }: SectionLabelProps) {
  return (
    <Tag
      className={`${className ? `${className} ` : ''}${SIZES[size]} ${bold ? 'font-semibold ' : ''}uppercase tracking-wide text-gray-500`}
    >
      {children}
    </Tag>
  )
}
