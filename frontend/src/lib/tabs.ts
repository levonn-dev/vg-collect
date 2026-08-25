// DOM id a Tabs entry renders on its tab button, for a tabpanel's
// aria-labelledby (WAI-ARIA tabs pattern). Kept out of Tabs.tsx: its
// fast-refresh boundary only tolerates component exports.
export function tabButtonId(panelId: string): string {
  return `${panelId}-tab`
}
