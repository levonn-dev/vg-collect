import { useMutation, useQueryClient } from '@tanstack/react-query'
import { useState } from 'react'
import { useNavigate, useSearchParams } from 'react-router'
import { createEntry } from '../api/collection'
import type { CatalogPick } from '../components/catalog/SearchPicker'
import SearchPicker from '../components/catalog/SearchPicker'
import ConfirmStep from '../components/wizard/ConfirmStep'
import type { CustomValues } from '../components/wizard/CustomStep'
import CustomStep from '../components/wizard/CustomStep'
import type { DetailsValues } from '../components/wizard/DetailsStep'
import DetailsStep, { detailsToCreate } from '../components/wizard/DetailsStep'
import { useDisplayMoney } from '../lib/useDisplayMoney'

type WizardStep =
  | { step: 'search' }
  | { step: 'details'; pick: CatalogPick }
  | { step: 'confirm'; pick: CatalogPick; details: DetailsValues }
  | { step: 'custom' }
  | { step: 'custom-details'; custom: CustomValues }
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
          onBack={() => setState({ step: 'search' })}
          onNext={(details) => setState({ ...state, step: 'confirm', details })}
        />
      )}
      {state.step === 'confirm' && (
        <ConfirmStep
          pick={state.pick}
          details={state.details}
          onBack={() => setState({ step: 'details', pick: state.pick })}
        />
      )}
      {state.step === 'custom' && (
        <CustomStep
          onBack={() => setState({ step: 'search' })}
          onNext={(custom) => setState({ step: 'custom-details', custom })}
        />
      )}
      {state.step === 'custom-details' && (
        <DetailsStep
          heading={`Your copy of ${state.custom.displayName}`}
          currency={money.profileCurrency}
          onBack={() => setState({ step: 'custom' })}
          onNext={(details) => setState({ ...state, step: 'custom-confirm', details })}
        />
      )}
      {state.step === 'custom-confirm' && (
        <CustomConfirm
          custom={state.custom}
          details={state.details}
          onBack={() => setState({ step: 'custom-details', custom: state.custom })}
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
    <section aria-label="Confirm custom item" className="flex flex-col gap-3">
      <h3 className="text-lg font-semibold">Confirm: {custom.displayName}</h3>
      <p className="text-sm text-gray-600">
        {[custom.platformName || null, custom.itemType, 'custom item'].filter(Boolean).join(' - ')}
      </p>
      <p className="rounded bg-gray-50 p-3 text-sm text-gray-600">
        Custom items start without market pricing. To track a value, open the entry afterwards
        and choose a similar listed item as its price source.
      </p>
      {create.isError && (
        <p role="alert" className="rounded bg-red-50 p-3 text-sm text-red-700">
          {create.error.message || 'The entry could not be created.'}
        </p>
      )}
      <div className="flex gap-2">
        <button onClick={onBack} className="rounded border border-gray-300 px-3 py-1 text-sm hover:bg-gray-50">
          Back
        </button>
        <button
          onClick={() => create.mutate()}
          disabled={create.isPending}
          className="rounded bg-gray-900 px-4 py-1 text-sm text-white enabled:hover:bg-gray-700 disabled:opacity-50"
        >
          Add to collection
        </button>
      </div>
    </section>
  )
}
