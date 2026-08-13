import type { ReactNode } from 'react'
import { Link } from 'react-router'
import type { EntryRow } from './rowMeta'

interface EntryLinkProps {
  entry: EntryRow
  // linkTo lets shared pages retarget or suppress row links; null
  // renders plain text. Same contract as EntryTable/CompactList/
  // CoverGrid's own linkTo prop - this component only resolves it.
  linkTo?: (e: EntryRow) => string | null
  // EntryTable and CompactList wrap the title in an inline span;
  // CoverGrid wraps a whole cover-plus-title block, which needs the
  // block-level tag Tailwind's own "block" utility on its plain
  // branch already assumed.
  as?: 'span' | 'div'
  plainClassName: string
  linkClassName: string
  children: ReactNode
}

// EntryLink resolves the link-or-plain-title conditional EntryTable,
// CompactList, and CoverGrid all repeat: linkTo(e) === null renders
// plain text (a read-only shared view of a row the viewer does not
// own), anything else - including the unset default - renders a Link
// to the entry detail page or the caller's own target.
export default function EntryLink({ entry, linkTo, as = 'span', plainClassName, linkClassName, children }: EntryLinkProps) {
  if (linkTo?.(entry) === null) {
    const Wrapper = as
    return <Wrapper className={plainClassName}>{children}</Wrapper>
  }
  const to = (linkTo ?? ((e: EntryRow) => `/entries/${e.id}`))(entry)!
  return <Link to={to} className={linkClassName}>{children}</Link>
}
