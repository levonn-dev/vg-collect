import { i18n } from '@lingui/core'
import { t } from '@lingui/core/macro'
import { formatLocale } from './locale'

// relativeTime buckets a timestamp for a byline - comments and feed
// rows both use it. now defaults to the real clock; tests pin it
// explicitly so the buckets are deterministic regardless of when the
// suite runs. Bucket strings live in the catalog; English output is
// unchanged ("3m ago" stays "3m ago").
export function relativeTime(iso: string, now: number = Date.now()): string {
  const minutes = Math.floor((now - new Date(iso).getTime()) / 60000)
  if (minutes < 1) return t(i18n)`just now`
  if (minutes < 60) return t(i18n)`${minutes}m ago`
  const hours = Math.floor(minutes / 60)
  if (hours < 24) return t(i18n)`${hours}h ago`
  const days = Math.floor(hours / 24)
  if (days < 7) return t(i18n)`${days}d ago`
  return new Date(iso).toLocaleDateString(formatLocale())
}
