import { Plural } from '@lingui/react/macro'
import { Link } from 'react-router'
import type { ShelfCard } from '../../api/social'
import UserChip from './UserChip'

// ShelfCard is the shared shelf summary tile: Explore grids, profile
// shelf lists, and feed excerpts all render the same card. owner rides
// along on the payload, so the byline (via UserChip) needs no second
// round trip. like_count/comment_count/viewer_likes are absent
// together exactly when the page's social composition failed open -
// only like_count is shown here, and only when present.
export default function ShelfCard({ card }: { card: ShelfCard }) {
  const entryCount = card.entry_count
  const likeCount = card.like_count ?? 0
  return (
    <div className="rounded border border-gray-200 p-2">
      {card.cover_urls.length > 0 && (
        <div className="mb-2 flex gap-1">
          {card.cover_urls.slice(0, 4).map((url, i) => (
            <img key={i} src={url} alt="" className="aspect-[3/4] w-1/4 rounded object-cover" />
          ))}
        </div>
      )}
      <Link
        to={`/u/${card.owner.handle}/shelves/${card.slug}`}
        className="font-medium hover:underline"
      >
        {card.name}
      </Link>
      <div className="mt-1">
        <UserChip profile={card.owner} />
      </div>
      <p className="mt-1 flex flex-wrap items-center gap-2 text-xs text-gray-500">
        <span><Plural value={entryCount} one="# entry" other="# entries" /></span>
        {card.like_count !== undefined && (
          <span><Plural value={likeCount} one="# like" other="# likes" /></span>
        )}
        {card.published_at && <span>{new Date(card.published_at).toLocaleDateString()}</span>}
      </p>
    </div>
  )
}
