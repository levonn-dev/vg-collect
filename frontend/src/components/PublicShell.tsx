import { useLingui } from '@lingui/react/macro'
import { Link, Outlet } from 'react-router'
import { site } from '../lib/site'
import { useMe } from '../lib/useMe'
import AppBar from './AppBar'
import Footer from './Footer'
import Logo from './Logo'
import SkipLink from './SkipLink'

// Shares Layout's ['me'] cache; never gates render, so pages paint even with
// the backend down, upgrading to the signed-in app bar once resolved.
// min-h-screen + flex-1 pins the footer to viewport bottom and lets Login center.
export default function PublicShell() {
  const { t } = useLingui()
  const me = useMe()
  return (
    <div className="mx-auto flex min-h-screen max-w-6xl flex-col p-4">
      <SkipLink />
      {me.data ? (
        <AppBar me={me.data} />
      ) : (
        <header
          className="flex items-center gap-2 border-b border-gray-200 pb-3"
          aria-label={t`App bar`}
        >
          {/* min-h-9 matches the authed app bar's Avatar chip (h-8 + py-0.5). */}
          <Link to="/" className="flex min-h-9 items-center gap-2">
            <Logo />
            <h1 className="text-xl font-bold">{site().name}</h1>
          </Link>
        </header>
      )}
      <div className="flex flex-1 flex-col">
        <Outlet />
      </div>
      <Footer showHelp={Boolean(me.data)} />
    </div>
  )
}
