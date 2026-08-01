import ProsePage from '../components/ProsePage'
import HelpEn from './help/Help.en'

// Help is prose: translated as a whole page per locale, never
// string-by-string. Contributing a language means adding
// help/Help.<locale>.tsx and registering it here.
export default function Help() {
  return <ProsePage variants={{ en: HelpEn }} page="help" />
}
