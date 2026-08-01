import { Trans, useLingui } from '@lingui/react/macro'
import { PAGE_SIZE } from '../../lib/listParams'

interface PagerProps {
  page: number
  totalCount: number
  onPage: (page: number) => void
}

export default function Pager({ page, totalCount, onPage }: PagerProps) {
  const { t } = useLingui()
  if (totalCount <= PAGE_SIZE && page === 0) {
    return <p className="mt-4 text-xs text-gray-500"><Trans>{totalCount} items</Trans></p>
  }
  const from = page * PAGE_SIZE + 1
  const to = Math.min((page + 1) * PAGE_SIZE, totalCount)
  const lastPage = Math.max(0, Math.ceil(totalCount / PAGE_SIZE) - 1)
  return (
    <nav className="mt-4 flex items-center gap-3 text-sm" aria-label={t`Pagination`}>
      <button
        onClick={() => onPage(page - 1)}
        disabled={page === 0}
        className="rounded border border-gray-300 px-2 py-1 enabled:hover:bg-gray-50 disabled:opacity-40"
      >
        <Trans>Previous</Trans>
      </button>
      <span className="text-xs text-gray-500">
        <Trans>{from}-{to} of {totalCount}</Trans>
      </span>
      <button
        onClick={() => onPage(page + 1)}
        disabled={page >= lastPage}
        className="rounded border border-gray-300 px-2 py-1 enabled:hover:bg-gray-50 disabled:opacity-40"
      >
        <Trans>Next</Trans>
      </button>
    </nav>
  )
}
