import { Trans, useLingui } from '@lingui/react/macro'
import { t } from '@lingui/core/macro'
import { useMutation, useQueryClient } from '@tanstack/react-query'
import { unlinkIdentity, type Identity } from '../../api/me'
import { confirmThen } from '../../lib/confirm'
import { formatDate } from '../../lib/format'
import { btnSecondary } from '../../lib/formStyles'

export default function LinkedLogins({ identities }: { identities: Identity[] }) {
  const { i18n } = useLingui()
  const queryClient = useQueryClient()
  const unlink = useMutation({
    mutationFn: unlinkIdentity,
    onSuccess: () => void queryClient.invalidateQueries({ queryKey: ['identities'] }),
  })
  const lastOne = identities.length === 1

  return (
    <ul className="flex max-w-md flex-col gap-2">
      {identities.map((identity) => {
        const email = identity.email ?? t(i18n)`no email recorded`
        const linkedDate = formatDate(identity.created_at)
        return (
          <li
            key={identity.id}
            className="flex items-center justify-between rounded border border-gray-200 px-3 py-2"
          >
            <div>
              <p className="text-sm font-medium capitalize">{identity.provider}</p>
              <p className="text-xs text-gray-500">
                <Trans>
                  {email} - linked{' '}
                  {linkedDate}
                </Trans>
              </p>
            </div>
            <button
              onClick={() =>
                confirmThen(
                  t(i18n)`Unlink this login? You will no longer be able to sign in with it.`,
                  () => unlink.mutate(identity.id),
                )
              }
              disabled={lastOne || unlink.isPending}
              title={lastOne ? t(i18n)`Your account needs at least one login` : undefined}
              className={btnSecondary}
            >
              <Trans>Unlink</Trans>
            </button>
          </li>
        )
      })}
      {unlink.isError && (
        <li role="alert" className="text-sm text-red-700">
          <Trans>Unlinking failed. Please try again.</Trans>
        </li>
      )}
    </ul>
  )
}
