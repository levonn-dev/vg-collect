// StringListInput's submit-time companion: trims, drops blanks,
// collapses empty to undefined so the wire carries no phantom array.
export function cleanNames(names: string[]): string[] | undefined {
  const out = names.map((n) => n.trim()).filter((n) => n !== '')
  return out.length > 0 ? out : undefined
}
