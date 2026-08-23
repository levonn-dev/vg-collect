import { msg } from '@lingui/core/macro'
import type { I18n, MessageDescriptor } from '@lingui/core'
import type { Region } from './listParams'

// Region display labels, split into their own leaf module so the entry
// form and the catalog search picker's region chip can both import
// them without either depending on the other: merging this back into
// EntryForm reopens the cycle SearchPicker -> EntryForm -> PricingPanel
// -> ProxyPicker -> SearchPicker.
export const regionLabels: Record<Region, MessageDescriptor> = {
  ntsc_u: msg`NTSC-U`,
  ntsc_j: msg`NTSC-J`,
  pal: msg`PAL`,
  korea: msg`Korea`,
  brazil: msg`Brazil`,
  china: msg`China`,
  region_free: msg`Region free`,
}

// Open-world regions render verbatim so an unknown value stays visible
// as the user wrote it; only the known regions have display labels.
export function regionLabelText(i18n: I18n, region: string): string {
  const d = (regionLabels as Record<string, MessageDescriptor>)[region]
  return d ? i18n._(d) : region
}
