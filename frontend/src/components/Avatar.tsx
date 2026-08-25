import { useState } from 'react'

type AvatarSize = 'sm' | 'md' | 'lg'

// sm=UserChip byline, md=Layout header chip, lg=profile page header.
const SIZES: Record<AvatarSize, { box: string; text: string }> = {
  sm: { box: 'h-6 w-6', text: 'text-xs' },
  md: { box: 'h-8 w-8', text: 'text-sm' },
  lg: { box: 'h-16 w-16', text: 'text-2xl' },
}

interface AvatarProps {
  url?: string
  // Used only for the fallback's first character; img alt stays empty so
  // label never appears as visible text.
  label: string
  size: AvatarSize
}

// <img> never retries after onError, so failure falls back to an initial glyph.
// no-referrer avoids googleusercontent's referrer-based rejection.
// Callers key by url so a changed avatar remounts instead of showing the stale one.
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
