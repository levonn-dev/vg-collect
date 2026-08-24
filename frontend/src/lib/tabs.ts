// tabButtonId is the DOM id a Tabs entry with a panelId renders on its
// own tab button (see components/Tabs.tsx) - the exact value a
// caller's tabpanel points its aria-labelledby at, completing the
// WAI-ARIA tabs pattern's tab<->panel relationship. Kept out of
// Tabs.tsx itself: a component file's fast-refresh boundary only
// tolerates component exports, and every caller building a tabpanel
// needs this same derivation, not just Tabs.tsx.
export function tabButtonId(panelId: string): string {
  return `${panelId}-tab`
}
