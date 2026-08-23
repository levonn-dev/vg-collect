import type { ReactNode } from 'react'

type EmptyStateSize = 'default' | 'compact'

// PADDING maps each named size to the exact vertical padding each call
// site needs: default (py-12) covers every page-level "nothing here"
// line (Explore, Profile, Feed, Collection, Recommendations); compact
// (py-8) matches SharedShelf's entries section, which sits inside an
// already-padded page rather than filling one on its own.
const PADDING: Record<EmptyStateSize, string> = {
  default: 'py-12',
  compact: 'py-8',
}

interface EmptyStateProps {
  size: EmptyStateSize
  children: ReactNode
}

// EmptyState is the centered-gray "nothing here" line shared by every
// list that can come back with zero rows.
export default function EmptyState({ size, children }: EmptyStateProps) {
  return <p className={`${PADDING[size]} text-center text-gray-500`}>{children}</p>
}
