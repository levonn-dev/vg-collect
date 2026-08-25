import ProsePage from '../components/ProsePage'
import HelpEn from './help/Help.en'
import HelpJa from './help/Help.ja'

// Prose translated as a whole page per locale, never string-by-string;
// a new language adds help/Help.<locale>.tsx and registers it here.
export default function Help() {
  return <ProsePage variants={{ en: HelpEn, ja: HelpJa }} page="help" />
}
