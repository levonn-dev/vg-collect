import { Trans, useLingui } from '@lingui/react/macro'
import { useMutation, useQueryClient } from '@tanstack/react-query'
import { updateMe, type Me } from '../api/me'
import { useFxRates } from '../lib/useDisplayMoney'
import { useMe } from '../lib/useMe'

// CurrencySelect sets the profile's display currency from the app
// header. The choice is optimistic: the cache flips immediately so
// every price re-renders at once, and a failed save rolls back. While
// rates are unavailable the app can only render USD, so the control
// pins there and says why.
export default function CurrencySelect() {
  const { t } = useLingui()
  const queryClient = useQueryClient()
  const me = useMe()
  const fx = useFxRates()

  const save = useMutation({
    mutationFn: (currency: string) => updateMe({ preferred_currency: currency }),
    onMutate: async (currency: string) => {
      await queryClient.cancelQueries({ queryKey: ['me'] })
      const previous = queryClient.getQueryData<Me>(['me'])
      if (previous) {
        queryClient.setQueryData<Me>(['me'], { ...previous, preferred_currency: currency })
      }
      return { previous }
    },
    onError: (_err, _currency, context) => {
      if (context?.previous) queryClient.setQueryData(['me'], context.previous)
    },
    onSettled: () => void queryClient.invalidateQueries({ queryKey: ['me'] }),
  })

  const options = fx.isSuccess ? ['USD', ...Object.keys(fx.data.rates).sort()] : ['USD']
  const current = me.data?.preferred_currency ?? 'USD'
  const value = options.includes(current) ? current : 'USD'

  return (
    <>
      <select
        aria-label={t`Display currency`}
        title={fx.isSuccess ? undefined : t`Exchange rates are unavailable; prices show in USD.`}
        value={value}
        disabled={!fx.isSuccess || save.isPending}
        onChange={(e) => save.mutate(e.target.value)}
        className="rounded border border-gray-300 px-2 py-1 text-sm text-gray-600 hover:bg-gray-50"
      >
        {options.map((c) => (
          <option key={c} value={c}>
            {c}
          </option>
        ))}
      </select>
      {save.isError && (
        <span role="alert" className="text-sm text-red-700">
          <Trans>Saving failed. Please try again.</Trans>
        </span>
      )}
    </>
  )
}
