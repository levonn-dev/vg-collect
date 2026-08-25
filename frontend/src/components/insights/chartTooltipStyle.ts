// Recharts' default tooltip box is hardcoded light; these vars keep it
// readable in dark mode. cursor stays per-chart (fill vs stroke, differing
// formatters) - only the box styling below is shared.
export const CHART_TOOLTIP_STYLE = {
  contentStyle: { backgroundColor: 'var(--color-white)', border: '1px solid var(--color-gray-300)' },
  labelStyle: { color: 'var(--color-gray-900)' },
}
