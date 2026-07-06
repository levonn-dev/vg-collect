import { useQuery } from '@tanstack/react-query'
import { Link } from 'react-router'
import { fetchRecommendations } from '../api/catalog'
import { releaseYear } from '../lib/format'

export default function Recommendations() {
  const recs = useQuery({ queryKey: ['recommendations'], queryFn: fetchRecommendations })

  if (recs.isPending) return <main className="py-8">Scoring your library...</main>
  if (recs.isError) {
    return (
      <main className="py-8" role="alert">
        Recommendations cannot be loaded right now. Please try again.
      </main>
    )
  }

  const { degraded, recommendations } = recs.data
  return (
    <main className="py-6" aria-label="Recommendations">
      <h2 className="mb-1 text-2xl font-bold">Recommended for you</h2>
      <p className="mb-4 text-sm text-gray-600">
        Unowned games scored against what you own, play, and rate.
      </p>
      {degraded && (
        <p role="alert" className="mb-4 rounded bg-amber-50 p-3 text-sm text-amber-800">
          Scoring ran degraded - some candidates were skipped. Try again later for a fuller list.
        </p>
      )}
      {recommendations.length === 0 ? (
        <p className="py-12 text-center text-gray-500">
          Nothing to recommend yet - add and rate a few games first.
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
                  className="mb-2 flex aspect-[3/4] w-full items-center justify-center rounded bg-gray-200 text-3xl font-bold text-gray-500"
                >
                  {r.name.charAt(0)}
                </div>
              )}
              <p className="text-sm font-medium">{r.name}</p>
              <p className="text-xs text-gray-500">
                {[releaseYear(r.first_release_date), r.genres.join(', ')].filter(Boolean).join(' - ')}
              </p>
              <p className="mt-1 text-xs text-gray-400">score {r.score.toFixed(1)}</p>
              <Link
                to={`/add?q=${encodeURIComponent(r.name)}`}
                aria-label={`Add ${r.name} to collection`}
                className="mt-2 rounded bg-gray-900 px-2 py-1 text-center text-xs text-white"
              >
                Add to collection
              </Link>
            </li>
          ))}
        </ul>
      )}
    </main>
  )
}
