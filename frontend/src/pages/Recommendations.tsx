import { Trans, useLingui } from '@lingui/react/macro'
import { useQuery } from '@tanstack/react-query'
import { Link } from 'react-router'
import { fetchRecommendations } from '../api/catalog'
import ItemTypeIcon from '../components/ItemTypeIcon'
import { releaseYear } from '../lib/format'

export default function Recommendations() {
  const { t } = useLingui()
  const recs = useQuery({ queryKey: ['recommendations'], queryFn: fetchRecommendations })

  if (recs.isPending) return <main className="py-8"><Trans>Scoring your library...</Trans></main>
  if (recs.isError) {
    return (
      <main className="py-8" role="alert">
        <Trans>Recommendations cannot be loaded right now. Please try again.</Trans>
      </main>
    )
  }

  const { degraded, recommendations } = recs.data
  return (
    <main className="py-6" aria-label={t`Recommendations`}>
      <h2 className="mb-1 text-2xl font-bold"><Trans>Recommended for you</Trans></h2>
      <p className="mb-4 text-sm text-gray-600">
        <Trans>Unowned games scored against what you own, play, and rate.</Trans>
      </p>
      {degraded && (
        <p role="alert" className="mb-4 rounded bg-amber-50 p-3 text-sm text-amber-800">
          <Trans>Scoring ran degraded - some candidates were skipped. Try again later for a fuller list.</Trans>
        </p>
      )}
      {recommendations.length === 0 ? (
        <p className="py-12 text-center text-gray-500">
          <Trans>Nothing to recommend yet - add and rate a few games first.</Trans>
        </p>
      ) : (
        <ul className="grid grid-cols-2 gap-4 sm:grid-cols-3 md:grid-cols-4 lg:grid-cols-5">
          {recommendations.map((r) => (
            <li key={r.igdb_game_id} className="flex flex-col rounded border border-gray-200 p-2">
              {r.cover_url ? (
                <img src={r.cover_url} alt="" className="mb-2 aspect-[3/4] w-full rounded object-cover" />
              ) : (
                <div
                  aria-hidden="true"
                  className="mb-2 flex aspect-[3/4] w-full items-center justify-center rounded bg-gray-100 text-gray-400"
                >
                  <ItemTypeIcon type="game" className="h-10 w-10" />
                </div>
              )}
              <p className="text-sm font-medium">{r.name}</p>
              <p className="text-xs text-gray-500">
                {[releaseYear(r.first_release_date), r.genres.join(', ')].filter(Boolean).join(' - ')}
              </p>
              <p className="mt-1 text-xs text-gray-400"><Trans>score {r.score.toFixed(1)}</Trans></p>
              <Link
                to={`/add?q=${encodeURIComponent(r.name)}`}
                aria-label={t`Add ${r.name} to collection`}
                className="mt-2 rounded bg-gray-900 px-2 py-1 text-center text-xs text-white hover:bg-gray-700"
              >
                <Trans>Add to collection</Trans>
              </Link>
            </li>
          ))}
        </ul>
      )}
    </main>
  )
}
