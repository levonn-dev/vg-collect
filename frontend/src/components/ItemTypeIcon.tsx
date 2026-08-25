// Decorative: the surrounding card carries the accessible name, so aria-hidden.
const glyphFor = (type?: string) => (type === 'console' || type === 'accessory' ? type : 'game')

export default function ItemTypeIcon({ type, className }: { type?: string; className?: string }) {
  const glyph = glyphFor(type)
  return (
    <svg
      viewBox="0 0 24 24"
      fill="none"
      stroke="currentColor"
      strokeWidth="1.5"
      strokeLinecap="round"
      strokeLinejoin="round"
      aria-hidden="true"
      data-icon={glyph}
      className={className}
    >
      {glyph === 'console' && (
        <>
          <rect x="3" y="8" width="18" height="9" rx="3" />
          <path d="M8 10.5v4M6 12.5h4" />
          <circle cx="15.5" cy="11.5" r="0.6" />
          <circle cx="18" cy="13.5" r="0.6" />
        </>
      )}
      {glyph === 'accessory' && (
        <>
          <path d="M9 3v4M15 3v4" />
          <path d="M7 7h10v4a5 5 0 0 1-10 0V7z" />
          <path d="M12 16v5" />
        </>
      )}
      {glyph === 'game' && (
        <>
          <path d="M6 3h12v13l-1.5 5h-9L6 16V3z" />
          <rect x="9" y="6" width="6" height="5" />
        </>
      )}
    </svg>
  )
}
