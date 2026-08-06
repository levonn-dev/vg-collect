import { msg } from '@lingui/core/macro'
import type { MessageDescriptor } from '@lingui/core'
import type { Entry } from '../api/collection'

// Region display labels, split into their own leaf module so the entry
// form and the catalog search picker's region chip can both import
// them without either depending on the other - SearchPicker importing
// this out of EntryForm used to close an import cycle (SearchPicker ->
// EntryForm -> PricingPanel -> ProxyPicker -> SearchPicker).
export const regionLabels: Record<Entry['region'], MessageDescriptor> = {
  ntsc_u: msg`NTSC-U`,
  ntsc_j: msg`NTSC-J`,
  pal: msg`PAL`,
  region_free: msg`Region free`,
}
