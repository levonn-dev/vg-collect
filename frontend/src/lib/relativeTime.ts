import { i18n } from '@lingui/core'
import { t } from '@lingui/core/macro'
import { formatDate } from './format'

// Buckets a timestamp for a byline (comments, feed rows); now defaults
// to the real clock, tests pin it for deterministic buckets.
export function relativeTime(iso: string, now: number = Date.now()): string {
  const minutes = Math.floor((now - new Date(iso).getTime()) / 60000)
  if (minutes < 1) return t(i18n)`just now`
  if (minutes < 60) return t(i18n)`${minutes}m ago`
  const hours = Math.floor(minutes / 60)
  if (hours < 24) return t(i18n)`${hours}h ago`
  const days = Math.floor(hours / 24)
  if (days < 7) return t(i18n)`${days}d ago`
  return formatDate(iso)
}
