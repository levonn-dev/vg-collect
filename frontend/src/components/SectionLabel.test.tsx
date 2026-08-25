import { screen } from '@testing-library/react'
import { renderWithI18n } from '../test/i18n'
import SectionLabel from './SectionLabel'

it('renders as the given element', () => {
  const { container } = renderWithI18n(<SectionLabel as="legend" size="xs">Platform</SectionLabel>)
  expect(container.querySelector('legend')).not.toBeNull()
})

it('defaults to bold (font-semibold) and omits it when bold is false', () => {
  renderWithI18n(<SectionLabel as="p" size="xs">Bold by default</SectionLabel>)
  expect(screen.getByText('Bold by default').className).toContain('font-semibold')

  renderWithI18n(<SectionLabel as="p" size="xs" bold={false}>Not bold</SectionLabel>)
  expect(screen.getByText('Not bold').className).not.toContain('font-semibold')
})

it('renders arbitrary children, not just plain text', () => {
  renderWithI18n(
    <SectionLabel as="h3" size="sm">
      Recommended <em>next</em>
    </SectionLabel>,
  )
  expect(screen.getByRole('heading', { level: 3 })).toHaveTextContent('Recommended next')
})

// Each case reproduces one of the seven class combos found across real
// sites, proving the four props recombine with no leftover/missing whitespace.
it.each([
  ['CommentList/Profile (h3, sm, bold, mb-3)', { as: 'h3', size: 'sm', className: 'mb-3' } as const,
    'mb-3 text-sm font-semibold uppercase tracking-wide text-gray-500'],
  ['PricingPanel (h3, sm, bold, no margin)', { as: 'h3', size: 'sm' } as const,
    'text-sm font-semibold uppercase tracking-wide text-gray-500'],
  ['ValueOverTime/StatCards (xs, bold, no margin)', { as: 'p', size: 'xs' } as const,
    'text-xs font-semibold uppercase tracking-wide text-gray-500'],
  ['BreakdownCharts/RecsPanel (xs, bold, mb-2)', { as: 'h3', size: 'xs', className: 'mb-2' } as const,
    'mb-2 text-xs font-semibold uppercase tracking-wide text-gray-500'],
  ['FilterBar/BulkEditBar (legend, xs, bold, float-left mr-2)', { as: 'legend', size: 'xs', className: 'float-left mr-2' } as const,
    'float-left mr-2 text-xs font-semibold uppercase tracking-wide text-gray-500'],
  ['Login/Account dev-fixture caption (xs, not bold, mb-2)', { as: 'p', size: 'xs', bold: false, className: 'mb-2' } as const,
    'mb-2 text-xs uppercase tracking-wide text-gray-500'],
  ['SharedShelf/Collection group heading (h3, sm, bold, mb-1)', { as: 'h3', size: 'sm', className: 'mb-1' } as const,
    'mb-1 text-sm font-semibold uppercase tracking-wide text-gray-500'],
])('%s', (_name, props, expectedClass) => {
  const { container } = renderWithI18n(<SectionLabel {...props}>Label</SectionLabel>)
  expect(container.firstElementChild?.className).toBe(expectedClass)
})
