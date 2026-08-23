import { Trans, useLingui } from '@lingui/react/macro'
import { useEffect } from 'react'
import type { ComponentType } from 'react'
import type { Locale } from '../lib/locale'
import { recordProseFallback } from '../telemetry'

export type ProseVariants = { en: ComponentType } & Partial<Record<Locale, ComponentType>>

// ProsePage serves whole-page translations: the active locale's
// variant when one was contributed, else the English page. A
// translated page carries a notice that English is the controlling
// text; that matters most on legal pages. English fallback shown to
// another locale gets no notice - nothing on it is a translation.
// page names the calling route ('about', 'terms', ...) for the
// fallback-served counter below; it carries no other meaning here.
export default function ProsePage({ variants, page }: { variants: ProseVariants; page: string }) {
  const { t, i18n } = useLingui()
  const active = i18n.locale as Locale
  const Variant = variants[active] ?? variants.en
  const translated = active !== 'en' && variants[active] !== undefined
  const fallback = active !== 'en' && !translated
  useEffect(() => {
    // StrictMode double-invokes effects in dev, so a dev session can
    // double-count one fallback-served page view; the counter is a
    // rate signal, not an exact-visits ledger, so this is accepted
    // rather than worked around.
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
      {/* Language of parts: an English page served under another
          locale is marked so assistive tech reads it with English
          pronunciation rules instead of the document's. */}
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
