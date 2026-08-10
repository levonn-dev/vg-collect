// cleanNames is StringListInput's submit-time companion: trim every
// row, drop the blanks, and collapse an empty result to undefined so
// the wire body carries no key at all rather than a phantom empty
// array.
export function cleanNames(names: string[]): string[] | undefined {
  const out = names.map((n) => n.trim()).filter((n) => n !== '')
  return out.length > 0 ? out : undefined
}
