import ProsePage from '../components/ProsePage'
import PrivacyEn from './privacy/Privacy.en'
import PrivacyJa from './privacy/Privacy.ja'

// Privacy is prose: translated as a whole page per locale, never
// string-by-string. Contributing a language means adding
// privacy/Privacy.<locale>.tsx and registering it here.
export default function Privacy() {
  return <ProsePage variants={{ en: PrivacyEn, ja: PrivacyJa }} page="privacy" />
}
