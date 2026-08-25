import type { ReactNode } from 'react'
import { Link } from 'react-router'
import type { EntryRow } from './rowMeta'

interface EntryLinkProps {
  entry: EntryRow
  // linkTo lets shared pages retarget or suppress row links; null renders
  // plain text. Same contract as EntryTable/CompactList/CoverGrid's own prop.
  linkTo?: (e: EntryRow) => string | null
  // EntryTable/CompactList wrap the title inline (span); CoverGrid wraps a
  // whole cover-plus-title block, which needs the block-level tag.
  as?: 'span' | 'div'
  plainClassName: string
  linkClassName: string
  children: ReactNode
}

// linkTo(e) === null renders plain text (read-only shared view); anything
// else, including the unset default, renders a Link.
export default function EntryLink({ entry, linkTo, as = 'span', plainClassName, linkClassName, children }: EntryLinkProps) {
  if (linkTo?.(entry) === null) {
    const Wrapper = as
    return <Wrapper className={plainClassName}>{children}</Wrapper>
  }
  const to = (linkTo ?? ((e: EntryRow) => `/entries/${e.id}`))(entry)!
  return <Link to={to} className={linkClassName}>{children}</Link>
}
