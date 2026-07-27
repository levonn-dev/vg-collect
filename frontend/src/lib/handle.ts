// Fold a handle to its identity key (case + underscores are
// decoration). Mirrors the server's NormalizeHandle; used ONLY for
// query-key stability - the server remains authoritative.
export function foldHandle(h: string): string {
  return h.toLowerCase().replaceAll('_', '')
}
