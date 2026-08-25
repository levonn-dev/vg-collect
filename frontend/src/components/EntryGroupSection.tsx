import type { ReactNode } from 'react'
import SectionLabel from './SectionLabel'

interface EntryGroupSectionProps {
  label: string
  children: ReactNode
}

// Inner render varies by caller (Collection vs SharedShelf), so it stays
// children rather than a typed prop.
export default function EntryGroupSection({ label, children }: EntryGroupSectionProps) {
  return (
    <section aria-label={label} className="mb-6">
      <SectionLabel as="h3" size="sm" className="mb-1">
        {label}
      </SectionLabel>
      {children}
    </section>
  )
}
