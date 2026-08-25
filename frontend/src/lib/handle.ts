// Mirrors the server's NormalizeHandle; used ONLY for query-key
// stability, server remains authoritative.
export function foldHandle(h: string): string {
  return h.toLowerCase().replaceAll('_', '')
}
