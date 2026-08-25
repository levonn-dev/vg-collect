import ProsePage from '../components/ProsePage'
import PrivacyEn from './privacy/Privacy.en'
import PrivacyJa from './privacy/Privacy.ja'

// Prose translated as a whole page per locale, never string-by-string;
// a new language adds privacy/Privacy.<locale>.tsx and registers it here.
export default function Privacy() {
  return <ProsePage variants={{ en: PrivacyEn, ja: PrivacyJa }} page="privacy" />
}
