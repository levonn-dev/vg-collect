import { useQuery } from '@tanstack/react-query'
import { Link, Outlet } from 'react-router'
import { fetchMe } from '../api/client'
import { site } from '../lib/site'
import AppBar from './AppBar'
import Footer from './Footer'
import Logo from './Logo'

// PublicShell frames every page reachable without a session. The
// session probe shares Layout's ['me'] cache and never gates render:
// pages paint immediately with the brand-only header (backend down
// included), and a resolved session only upgrades the chrome to the
// signed-in app bar so the header does not change with the route
// after sign-in. min-h-screen plus flex-1 keeps the footer at the
// viewport bottom on short pages and lets Login center vertically.
export default function PublicShell() {
  const me = useQuery({ queryKey: ['me'], queryFn: fetchMe })
  return (
    <div className="mx-auto flex min-h-screen max-w-6xl flex-col p-4">
      {me.data ? (
        <AppBar me={me.data} />
      ) : (
        <header
          className="flex items-center gap-2 border-b border-gray-200 pb-3"
          aria-label="App bar"
        >
          {/* min-h-9 matches the authed app bar's tallest chip (Avatar
              h-8 in its py-0.5 link) so both headers line up. */}
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
