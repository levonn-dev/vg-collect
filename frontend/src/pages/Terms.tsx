import ProsePage from '../components/ProsePage'
import TermsEn from './terms/Terms.en'
import TermsJa from './terms/Terms.ja'

// Terms is prose: translated as a whole page per locale, never
// string-by-string. Contributing a language means adding
// terms/Terms.<locale>.tsx and registering it here.
export default function Terms() {
  return <ProsePage variants={{ en: TermsEn, ja: TermsJa }} page="terms" />
}
