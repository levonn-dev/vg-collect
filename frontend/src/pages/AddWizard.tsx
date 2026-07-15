import { useMutation, useQueryClient } from '@tanstack/react-query'
import { useState } from 'react'
import { useNavigate, useSearchParams } from 'react-router'
import { createEntry } from '../api/collection'
import type { CatalogPick } from '../components/catalog/SearchPicker'
import SearchPicker from '../components/catalog/SearchPicker'
import ConfirmShell from '../components/wizard/ConfirmShell'
import ConfirmStep from '../components/wizard/ConfirmStep'
import type { CustomValues } from '../components/wizard/CustomStep'
import CustomStep from '../components/wizard/CustomStep'
import type { DetailsValues } from '../components/wizard/DetailsStep'
import DetailsStep, { detailsToCreate } from '../components/wizard/DetailsStep'
import type { ManualMatch } from '../lib/catalog'
import { useDisplayMoney } from '../lib/useDisplayMoney'

type WizardStep =
  | { step: 'search' }
  | { step: 'details'; pick: CatalogPick; details?: DetailsValues; manualMatch?: ManualMatch }
  | { step: 'confirm'; pick: CatalogPick; details: DetailsValues; manualMatch?: ManualMatch }
  | { step: 'custom'; custom?: CustomValues; details?: DetailsValues }
  | { step: 'custom-details'; custom: CustomValues; details?: DetailsValues }
  | { step: 'custom-confirm'; custom: CustomValues; details: DetailsValues }

export default function AddWizard() {
  const [searchParams] = useSearchParams()
  const [state, setState] = useState<WizardStep>({ step: 'search' })
  const money = useDisplayMoney()

  return (
    <main className="py-6" aria-label="Add to collection">
      <h2 className="mb-4 text-2xl font-bold">Add to collection</h2>
      {state.step === 'search' && (
        <SearchPicker
          initialQuery={searchParams.get('q') ?? ''}
          onPick={(pick) => setState({ step: 'details', pick })}
          footer={
            <p className="mt-2 border-t border-gray-100 pt-3 text-sm">
              <button
                type="button"
                onClick={() => setState({ step: 'custom' })}
                className="underline hover:text-gray-600"
              >
                Can not find it? Add it as a custom item.
              </button>
            </p>
          }
        />
      )}
      {state.step === 'details' && (
        <DetailsStep
          heading={`Your copy of ${state.pick.name}`}
          currency={money.profileCurrency}
          initialValues={state.details}
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
          onBack={() => setState({ step: 'search' })}
          onNext={(custom) => setState({ step: 'custom-details', custom, details: state.details })}
        />
      )}
      {state.step === 'custom-details' && (
        <DetailsStep
          heading={`Your copy of ${state.custom.displayName}`}
          currency={money.profileCurrency}
          initialValues={state.details}
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
  const navigate = useNavigate()
  const queryClient = useQueryClient()
  const money = useDisplayMoney()
  const create = useMutation({
    mutationFn: () =>
      createEntry({
        ...detailsToCreate(details, money.profileCurrency),
        pricing_mode: 'disabled',
        display_name: custom.displayName,
        item_type: custom.itemType,
        platform_name: custom.platformName.trim() === '' ? undefined : custom.platformName.trim(),
        first_release_date: custom.firstReleaseDate === '' ? undefined : custom.firstReleaseDate,
      }),
    onSuccess: (entry) => {
      void queryClient.invalidateQueries({ queryKey: ['entries'] })
      void queryClient.invalidateQueries({ queryKey: ['dashboard'] })
      void queryClient.invalidateQueries({ queryKey: ['recommendations'] })
      void navigate(`/entries/${entry.id}`, { state: { justAdded: true } })
    },
  })
  return (
    <ConfirmShell
      ariaLabel="Confirm custom item"
      title={custom.displayName}
      subtitle={[custom.platformName || null, custom.itemType, 'custom item'].filter(Boolean).join(' - ')}
      errorMessage={create.isError ? create.error.message || 'The entry could not be created.' : undefined}
      onBack={onBack}
      onSubmit={() => create.mutate()}
      submitPending={create.isPending}
    >
      <p className="rounded bg-gray-50 p-3 text-sm text-gray-600">
        Custom items start without market pricing. To track a value, open the entry afterwards
        and choose a similar listed item as its price source.
      </p>
    </ConfirmShell>
  )
}
