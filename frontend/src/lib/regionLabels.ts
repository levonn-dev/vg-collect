import { msg } from '@lingui/core/macro'
import type { I18n, MessageDescriptor } from '@lingui/core'
import type { Region } from './listParams'

// Split into its own leaf module so EntryForm and SearchPicker's
// region chip can both import it without a cycle (SearchPicker ->
// EntryForm -> PricingPanel -> ProxyPicker -> SearchPicker).
export const regionLabels: Record<Region, MessageDescriptor> = {
  ntsc_u: msg`NTSC-U`,
  ntsc_j: msg`NTSC-J`,
  pal: msg`PAL`,
  korea: msg`Korea`,
  brazil: msg`Brazil`,
  china: msg`China`,
  region_free: msg`Region free`,
}

// Open-world regions render verbatim (unknown value stays visible);
// only known regions have labels.
export function regionLabelText(i18n: I18n, region: string): string {
  const d = (regionLabels as Record<string, MessageDescriptor>)[region]
  return d ? i18n._(d) : region
}
