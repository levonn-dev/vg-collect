import { Trans, useLingui } from '@lingui/react/macro'
import { msg } from '@lingui/core/macro'
import type { I18n, MessageDescriptor } from '@lingui/core'
import { useMutation, useQueryClient } from '@tanstack/react-query'
import { useState } from 'react'
import { useNavigate, useSearchParams } from 'react-router'
import { createEntry } from '../api/collection'
import type { CatalogPick, SearchPickerState } from '../lib/catalogPicks'
import SearchPicker from '../components/catalog/SearchPicker'
import ConfirmShell from '../components/wizard/ConfirmShell'
import ConfirmStep from '../components/wizard/ConfirmStep'
import { cleanNames } from '../lib/credits'
import { itemTypeWireLabels } from '../lib/enumLabels'
import { invalidateEntryQueries } from '../lib/entryQueries'
import { resolveApiError } from '../lib/resolveApiError'
import type { CustomValues } from '../components/wizard/CustomStep'
import CustomStep from '../components/wizard/CustomStep'
import type { DetailsValues } from '../components/wizard/DetailsStep'
import DetailsStep, { defaultDetails, detailsToCreate } from '../components/wizard/DetailsStep'
import type { ManualMatch } from '../lib/catalog'
import { useDisplayMoney } from '../lib/useDisplayMoney'

// A custom create never references a catalog product: pricing_mode is
// hard-coded to disabled below and no product_id is ever sent, so
// createEntry cannot answer any of its documented codes on this path
// (unlike ConfirmStep's catalog-pick create, which reaches a couple)
// - the translated fallback is the whole story here.
const customEntryErrorCodes: Record<string, MessageDescriptor> = {}
function customEntryErrorMessage(e: unknown, i18n: I18n): string {
  return resolveApiError(e, i18n, customEntryErrorCodes, msg`The entry could not be created.`)
}

type WizardStep =
  | { step: 'search' }
  | { step: 'details'; pick: CatalogPick; details?: DetailsValues; manualMatch?: ManualMatch }
  | { step: 'confirm'; pick: CatalogPick; details: DetailsValues; manualMatch?: ManualMatch }
  | { step: 'custom'; custom?: CustomValues; details?: DetailsValues }
  | { step: 'custom-details'; custom: CustomValues; details?: DetailsValues }
  | { step: 'custom-confirm'; custom: CustomValues; details: DetailsValues }

export default function AddWizard() {
  const { t } = useLingui()
  const [searchParams] = useSearchParams()
  const [state, setState] = useState<WizardStep>({ step: 'search' })
  const money = useDisplayMoney()
  // Survives the step machine: SearchPicker unmounts on every step
  // change, so its state lives here and Back re-seeds it (the TanStack
  // search cache brings the results straight back). Wins over the ?q=
  // deep link, which only seeds a fresh wizard.
  const [searchState, setSearchState] = useState<SearchPickerState | undefined>(undefined)

  return (
    <main className="py-6" aria-label={t`Add to collection`}>
      <h2 className="mb-4 text-2xl font-bold"><Trans>Add to collection</Trans></h2>
      {state.step === 'search' && (
        <SearchPicker
          initialQuery={searchParams.get('q') ?? ''}
          initialState={searchState}
          onStateChange={setSearchState}
          onPick={(pick) => setState({ step: 'details', pick })}
          footer={
            <p className="mt-2 border-t border-gray-100 pt-3 text-sm">
              <button
                type="button"
                onClick={() => setState({ step: 'custom' })}
                className="underline hover:text-gray-600"
              >
                <Trans>Can not find it? Add it as a custom item.</Trans>
              </button>
            </p>
          }
        />
      )}
      {state.step === 'details' && (
        <DetailsStep
          product={
            state.pick.kind === 'game'
              ? { name: state.pick.name, localizations: state.pick.localizations }
              : { name: state.pick.name }
          }
          regionGroup={
            state.pick.kind === 'game' && state.pick.regions !== undefined
              ? { platformName: state.pick.platformName, regions: state.pick.regions }
              : undefined
          }
          currency={money.profileCurrency}
          initialValues={state.details ?? (state.pick.kind === 'game' || state.pick.kind === 'hardware' ? defaultDetails(state.pick.suggestedRegion) : state.pick.kind === 'community' && state.pick.region !== undefined ? defaultDetails(state.pick.region) : undefined)}
          manualMatch={state.pick.kind === 'game' ? state.manualMatch : undefined}
          onManualMatchChange={
            state.pick.kind === 'game' ? (m) => setState({ ...state, manualMatch: m ?? undefined }) : undefined
          }
          manualMatchQuery={state.pick.kind === 'game' ? state.pick.name : undefined}
          onBack={() => setState({ step: 'search' })}
          onNext={(details) => setState({ ...state, step: 'confirm', details })}
        />
      )}
      {state.step === 'confirm' && (
        <ConfirmStep
          pick={state.pick}
          details={state.details}
          manualMatch={state.manualMatch}
          onManualMatch={(m) => setState({ ...state, manualMatch: m })}
          onBack={() => setState({ step: 'details', pick: state.pick, details: state.details, manualMatch: state.manualMatch })}
        />
      )}
      {state.step === 'custom' && (
        <CustomStep
          initialValues={state.custom}
          seed={{
            displayName: searchState?.text ?? searchParams.get('q') ?? '',
            itemType: searchState?.kind === 'hardware' ? 'accessory' : 'game',
          }}
          onBack={() => setState({ step: 'search' })}
          onNext={(custom) => setState({ step: 'custom-details', custom, details: state.details })}
        />
      )}
      {state.step === 'custom-details' && (
        <DetailsStep
          product={{ name: state.custom.displayName }}
          currency={money.profileCurrency}
          initialValues={state.details ?? (state.custom.region !== '' ? defaultDetails(state.custom.region) : undefined)}
          onBack={() => setState({ step: 'custom', custom: state.custom, details: state.details })}
          onNext={(details) => setState({ ...state, step: 'custom-confirm', details })}
        />
      )}
      {state.step === 'custom-confirm' && (
        <CustomConfirm
          custom={state.custom}
          details={state.details}
          onBack={() => setState({ step: 'custom-details', custom: state.custom, details: state.details })}
        />
      )}
    </main>
  )
}

function CustomConfirm({
  custom, details, onBack,
}: {
  custom: CustomValues
  details: DetailsValues
  onBack: () => void
}) {
  const { t, i18n } = useLingui()
  const navigate = useNavigate()
  const queryClient = useQueryClient()
  const money = useDisplayMoney()
  const create = useMutation({
    mutationFn: () =>
      createEntry({
        ...detailsToCreate(details, money.profileCurrency),
        pricing_mode: 'disabled',
        match_provenance: 'auto',
        display_name: custom.displayName,
        item_type: custom.itemType,
        platform_name: custom.platformName.trim() === '' ? undefined : custom.platformName.trim(),
        platform_igdb_id: custom.platformIgdbId,
        first_release_date: custom.firstReleaseDate === '' ? undefined : custom.firstReleaseDate,
        cover_url: custom.coverUrl.trim() === '' ? undefined : custom.coverUrl.trim(),
        developers: cleanNames(custom.developers),
        publishers: cleanNames(custom.publishers),
      }),
    onSuccess: (entry) => {
      // A custom add can mint new facet values (its platform and its
      // credit names) - same invalidation the product-add confirm does.
      invalidateEntryQueries(queryClient, [['entry-facets']])
      void navigate(`/entries/${entry.id}`, { state: { justAdded: true } })
    },
  })
  return (
    <ConfirmShell
      ariaLabel={t`Confirm custom item`}
      title={custom.displayName}
      subtitle={[custom.platformName || null, i18n._(itemTypeWireLabels[custom.itemType]), t`custom item`].filter(Boolean).join(' - ')}
      errorMessage={create.isError ? customEntryErrorMessage(create.error, i18n) : undefined}
      onBack={onBack}
      onSubmit={() => create.mutate()}
      submitPending={create.isPending}
    >
      <p className="rounded bg-gray-50 p-3 text-sm text-gray-600">
        <Trans>
          Custom items start without market pricing. To track a value, open the entry afterwards
          and choose a similar listed item as its price source.
        </Trans>
      </p>
    </ConfirmShell>
  )
}
