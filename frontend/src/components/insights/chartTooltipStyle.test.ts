import { CHART_TOOLTIP_STYLE } from './chartTooltipStyle'

it('carries the exact box styles BreakdownCharts and ValueOverTime shared', () => {
  expect(CHART_TOOLTIP_STYLE).toEqual({
    contentStyle: { backgroundColor: 'var(--color-white)', border: '1px solid var(--color-gray-300)' },
    labelStyle: { color: 'var(--color-gray-900)' },
  })
})
