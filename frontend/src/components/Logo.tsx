// docs/brand/logo.svg is the canonical source. Decorative (h1 carries the
// accessible name); indigo is a fixed brand color, not a theme token.
export default function Logo({ className = 'h-7 w-auto' }: { className?: string }) {
  return (
    <svg viewBox="0 0 120 90" width="40" height="30" aria-hidden="true" className={className}>
      <rect width="120" height="90" rx="12" fill="#4F46E5" />
      <g fill="#FFFFFF">
        <rect x="20" y="20" width="10" height="40" />
        <rect x="40" y="20" width="10" height="40" />
        <rect x="30" y="60" width="10" height="10" />
        <rect x="70" y="20" width="30" height="10" />
        <rect x="60" y="30" width="10" height="30" />
        <path d="M80 40H100V70H70V60H90V50H80Z" />
      </g>
    </svg>
  )
}
