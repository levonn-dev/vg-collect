import { useLingui } from '@lingui/react/macro'
import { LOCALE_NAMES, SUPPORTED_LOCALES, setLocale, type Locale } from '../lib/locale'

// LocaleSwitch is the language picker. It renders nothing until at
// least two locales exist, so shipping it with English alone changes
// no visible UI. The locales prop exists for tests; production always
// uses the default.
export default function LocaleSwitch({
  locales = SUPPORTED_LOCALES,
}: {
  locales?: readonly Locale[]
}) {
  const { t, i18n } = useLingui()
  if (locales.length < 2) return null
  return (
    <select
      aria-label={t`Language`}
      value={i18n.locale}
      onChange={(e) => void setLocale(e.target.value as Locale)}
      className="rounded border border-gray-300 px-2 py-1 text-sm text-gray-600 hover:bg-gray-50"
    >
      {locales.map((l) => (
        <option key={l} value={l}>
          {LOCALE_NAMES[l]}
        </option>
      ))}
    </select>
  )
}
