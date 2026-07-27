import { Link } from 'react-router'
import type { ProfileCard } from '../../api/social'
import Avatar from '../Avatar'

// UserChip is the cross-surface identity chip - avatar plus @handle,
// linking to the public profile - shared by feed rows, shelf card
// bylines, and search results.
export default function UserChip({ profile }: { profile: ProfileCard }) {
  return (
    <Link
      to={`/u/${profile.handle}`}
      className="inline-flex items-center gap-1.5 text-sm hover:underline"
    >
      <Avatar key={profile.avatar_url} url={profile.avatar_url} label={profile.handle} size="sm" />
      <span className="font-medium text-gray-900">@{profile.handle}</span>
    </Link>
  )
}
