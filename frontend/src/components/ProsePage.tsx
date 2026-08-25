import { msg } from '@lingui/core/macro'
import { Trans, useLingui } from '@lingui/react/macro'
import type { MessageDescriptor } from '@lingui/core'
import { useEffect } from 'react'
import type { ComponentType } from 'react'
import type { Locale } from '../lib/locale'
import { useDocumentTitle } from '../lib/useDocumentTitle'
import { recordProseFallback } from '../telemetry'

export type ProseVariants = { en: ComponentType } & Partial<Record<Locale, ComponentType>>

// Keyed by the page names callers pass; unknown pages fall back to the app title.
const pageTitles: Record<string, MessageDescriptor> = {
  about: msg`About`,
  terms: msg`Terms`,
  privacy: msg`Privacy`,
  help: msg`Help`,
}

// Active locale's variant when contributed, else English with no notice
// (fallback, not translation); a real translation notes English is controlling.
// page names the route, used only by the fallback-served counter below.
export default function ProsePage({ variants, page }: { variants: ProseVariants; page: string }) {
  const { t, i18n } = useLingui()
  // Every real caller has an entry above; the fallback string never renders,
  // so it isn't pulled through msg extraction.
  useDocumentTitle(pageTitles[page] ? i18n._(pageTitles[page]) : 'vgkeep')
  const active = i18n.locale as Locale
  const Variant = variants[active] ?? variants.en
  const translated = active !== 'en' && variants[active] !== undefined
  const fallback = active !== 'en' && !translated
  useEffect(() => {
    // StrictMode double-invokes effects in dev, double-counting one page view;
    // accepted since the counter is a rate signal, not an exact ledger.
    if (fallback) recordProseFallback(page)
  }, [fallback, page])
  return (
    <>
      {translated && (
        <aside
          aria-label={t`About this translation`}
          className="mx-auto w-full max-w-2xl px-6 pt-6 text-sm text-gray-600"
        >
          <Trans>
            This translation is provided for convenience. The English version is the
            controlling text.
          </Trans>
        </aside>
      )}
      {/* lang=en: an English fallback page reads with English pronunciation
          rules, not the document's. */}
      {fallback ? (
        <div lang="en">
          <Variant />
        </div>
      ) : (
        <Variant />
      )}
    </>
  )
}
