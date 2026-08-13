// Recharts' default hover band/line and tooltip box are hardcoded
// light; these theme variables keep the mouse-over readable in dark
// mode. cursor stays per-chart (BreakdownCharts fills a hover band,
// ValueOverTime strokes a hover line, and Tooltip's formatter differs
// too) - only the box styling below is identical between the two.
export const CHART_TOOLTIP_STYLE = {
  contentStyle: { backgroundColor: 'var(--color-white)', border: '1px solid var(--color-gray-300)' },
  labelStyle: { color: 'var(--color-gray-900)' },
}
