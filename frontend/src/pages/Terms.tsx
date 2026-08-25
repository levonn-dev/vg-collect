import ProsePage from '../components/ProsePage'
import TermsEn from './terms/Terms.en'
import TermsJa from './terms/Terms.ja'

// Prose translated as a whole page per locale, never string-by-string;
// a new language adds terms/Terms.<locale>.tsx and registers it here.
export default function Terms() {
  return <ProsePage variants={{ en: TermsEn, ja: TermsJa }} page="terms" />
}
