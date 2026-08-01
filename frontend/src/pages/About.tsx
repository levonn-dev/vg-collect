import ProsePage from '../components/ProsePage'
import AboutEn from './about/About.en'

// About is prose: translated as a whole page per locale, never
// string-by-string. Contributing a language means adding
// about/About.<locale>.tsx and registering it here.
export default function About() {
  return <ProsePage variants={{ en: AboutEn }} page="about" />
}
