import { Trans, useLingui } from '@lingui/react/macro'
import { useQuery } from '@tanstack/react-query'
import { Link } from 'react-router'
import { fetchRecommendations } from '../../api/catalog'

export default function RecsPanel() {
  const { t } = useLingui()
  const recs = useQuery({ queryKey: ['recommendations'], queryFn: fetchRecommendations })
  return (
    <section aria-label={t`Recommendations`} className="rounded border border-gray-200 p-4">
      <h3 className="mb-2 text-xs font-semibold uppercase tracking-wide text-gray-500">
        <Trans>Recommended next</Trans>
      </h3>
      {recs.isPending && <p className="text-sm text-gray-500"><Trans>Scoring your library...</Trans></p>}
      {recs.isError && (
        <p className="text-sm text-gray-500"><Trans>Recommendations are unavailable right now.</Trans></p>
      )}
      {recs.data && recs.data.recommendations.length === 0 && (
        <p className="text-sm text-gray-500"><Trans>Add and rate a few games to get suggestions.</Trans></p>
      )}
      <ul className="flex flex-col gap-1 text-sm">
        {recs.data?.recommendations.slice(0, 5).map((r) => (
          <li key={r.igdb_game_id} className="flex justify-between">
            <span>{r.name}</span>
            <span className="text-gray-400">{r.score.toFixed(1)}</span>
          </li>
        ))}
      </ul>
      <Link to="/recommendations" className="mt-2 inline-block text-sm underline">
        <Trans>See all recommendations</Trans>
      </Link>
    </section>
  )
}
