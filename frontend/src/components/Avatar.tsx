import { useState } from 'react'

type AvatarSize = 'sm' | 'md' | 'lg'

// SIZES maps each named size to the exact box + initial-glyph classes
// each call site needs: UserChip's inline byline (sm, 24px), Layout's
// header identity chip (md, 32px), Profile's page header (lg, 64px).
const SIZES: Record<AvatarSize, { box: string; text: string }> = {
  sm: { box: 'h-6 w-6', text: 'text-xs' },
  md: { box: 'h-8 w-8', text: 'text-sm' },
  lg: { box: 'h-16 w-16', text: 'text-2xl' },
}

interface AvatarProps {
  url?: string
  // Name/handle-agnostic: whichever identity string the initial
  // fallback should read the first character of. The image itself
  // stays decorative (alt=""); label never renders as visible text.
  label: string
  size: AvatarSize
}

// Avatar renders a provider profile image with a same-size initial
// fallback: third-party avatar hosts flake (aborted first loads,
// referrer-sensitive throttling), and a failed <img> never retries on
// its own, so a failure must degrade to something stable instead of a
// stuck blank. no-referrer sidesteps googleusercontent's
// referrer-based rejections. Callers key the element by the url so a
// changed avatar remounts with a fresh attempt instead of staying
// stuck on a stale failure.
export default function Avatar({ url, label, size }: AvatarProps) {
  const [failed, setFailed] = useState(false)
  const { box, text } = SIZES[size]
  if (!url || failed) {
    return (
      <span
        aria-hidden="true"
        className={`flex ${box} items-center justify-center rounded-full bg-gray-200 ${text} font-bold text-gray-500`}
      >
        {label.charAt(0)}
      </span>
    )
  }
  return (
    <img
      src={url}
      alt=""
      referrerPolicy="no-referrer"
      onError={() => setFailed(true)}
      className={`${box} rounded-full`}
    />
  )
}
