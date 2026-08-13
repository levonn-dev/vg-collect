import type { ReactNode } from 'react'

type SectionLabelElement = 'h3' | 'p' | 'legend'

interface SectionLabelProps {
  as: SectionLabelElement
  size: 'xs' | 'sm'
  // Every site is font-semibold except Login/Account's "Dev fixtures"
  // caption, which sits under a divider as a lighter secondary hint
  // rather than opening its own section.
  bold?: boolean
  // The one thing that differs beyond size/weight/element: a top
  // margin (mb-1/mb-2/mb-3), FilterBar/BulkEditBar's float-left mr-2
  // (the legend floats beside its fieldset's own first row), or
  // nothing (PricingPanel, ValueOverTime, StatCards sit flush against
  // their section's own padding). Prepended ahead of the shared
  // classes, same convention as LoadMoreButton's className.
  className?: string
  children: ReactNode
}

// SectionLabel is the uppercase, tracking-wide caption shared by every
// section heading, stat label, and fieldset legend: a page's h3
// subsection title, a stat card's label, and a filter fieldset's
// legend all read as the same small-caps-style caption, just at a
// different size/weight/element/margin per site.
export default function SectionLabel({ as: Tag, size, bold = true, className, children }: SectionLabelProps) {
  return (
    <Tag
      className={`${className ? `${className} ` : ''}text-${size} ${bold ? 'font-semibold ' : ''}uppercase tracking-wide text-gray-500`}
    >
      {children}
    </Tag>
  )
}
