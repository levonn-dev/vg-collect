import type { ReactNode } from 'react'

type EmptyStateSize = 'default' | 'compact'

// default = page-level "nothing here" lines; compact = sections nested inside
// an already-padded page (e.g. SharedShelf's entries).
const PADDING: Record<EmptyStateSize, string> = {
  default: 'py-12',
  compact: 'py-8',
}

interface EmptyStateProps {
  size: EmptyStateSize
  children: ReactNode
}

// Centered gray "nothing here" line shared by every zero-row list.
export default function EmptyState({ size, children }: EmptyStateProps) {
  return <p className={`${PADDING[size]} text-center text-gray-500`}>{children}</p>
}
