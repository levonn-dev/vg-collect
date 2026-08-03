import ProsePage from '../components/ProsePage'
import HelpEn from './help/Help.en'
import HelpJa from './help/Help.ja'

// Help is prose: translated as a whole page per locale, never
// string-by-string. Contributing a language means adding
// help/Help.<locale>.tsx and registering it here.
export default function Help() {
  return <ProsePage variants={{ en: HelpEn, ja: HelpJa }} page="help" />
}
