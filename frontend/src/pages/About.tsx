import ProsePage from '../components/ProsePage'
import AboutEn from './about/About.en'
import AboutJa from './about/About.ja'

// Prose translated as a whole page per locale, never string-by-string;
// a new language adds about/About.<locale>.tsx and registers it here.
export default function About() {
  return <ProsePage variants={{ en: AboutEn, ja: AboutJa }} page="about" />
}
