import type { ReactNode } from 'react'
import SectionLabel from './SectionLabel'

interface EntryGroupSectionProps {
  label: string
  children: ReactNode
}

// EntryGroupSection is the grouped-entry-list heading wrapper Collection
// and SharedShelf both repeat around their own entry rendering (a plain
// EntryTable/CoverGrid/CompactList call for Collection, a read-only
// linkTo-suppressed View for SharedShelf) - only the inner render
// differs, so it stays the caller's children rather than a prop this
// component would have to know the shape of.
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
