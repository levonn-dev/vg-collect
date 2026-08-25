import { Trans, useLingui } from '@lingui/react/macro'
import { useMutation, useQueryClient } from '@tanstack/react-query'
import { Link, NavLink, useNavigate } from 'react-router'
import { logout, type Me } from '../api/me'
import { btnSecondary } from '../lib/formStyles'
import { site } from '../lib/site'
import Avatar from './Avatar'
import CurrencySelect from './CurrencySelect'
import Logo from './Logo'
import ThemeToggle from './ThemeToggle'

function navClass({ isActive }: { isActive: boolean }): string {
  return isActive
    ? 'text-sm font-semibold text-gray-900'
    : 'text-sm text-gray-500 hover:text-gray-900'
}

// Also rendered by PublicShell once its session probe resolves, so chrome
// stays static across the sign-in route.
export default function AppBar({ me }: { me: Me }) {
  const { t } = useLingui()
  const queryClient = useQueryClient()
  const navigate = useNavigate()
  const signOut = useMutation({
    mutationFn: logout,
    // Fires on both HTTP and network failure; either way the session is gone.
    onSettled: () => {
      queryClient.clear()
      void navigate('/login')
    },
  })

  return (
    <header
      className="flex items-center justify-between border-b border-gray-200 pb-3"
      aria-label={t`App bar`}
    >
      <div className="flex items-baseline gap-6">
        <Link to="/" className="flex items-center gap-2">
          <Logo />
          <h1 className="text-xl font-bold">{site().name}</h1>
        </Link>
        <nav className="flex gap-4" aria-label={t`Primary`}>
          <NavLink to="/collection" end className={navClass}>
            <Trans>Collection</Trans>
          </NavLink>
          <NavLink to="/add" className={navClass}>
            <Trans>Add</Trans>
          </NavLink>
          <NavLink to="/explore" className={navClass}>
            <Trans>Explore</Trans>
          </NavLink>
          <NavLink to="/feed" className={navClass}>
            <Trans>Feed</Trans>
          </NavLink>
          <NavLink to="/recommendations" className={navClass}>
            <Trans>Recommendations</Trans>
          </NavLink>
          {me.roles.includes('admin') && (
            <NavLink to="/admin" className={navClass}>
              <Trans>Admin</Trans>
            </NavLink>
          )}
        </nav>
      </div>
      <div className="flex items-center gap-3">
        <CurrencySelect />
        <ThemeToggle />
        <NavLink
          to="/account"
          aria-label={t`Account`}
          className="flex items-center gap-3 rounded px-1 py-0.5 hover:bg-gray-50"
        >
          <Avatar key={me.avatar_url} url={me.avatar_url} label={me.handle} size="md" />
          <span className="text-sm text-gray-700">@{me.handle}</span>
        </NavLink>
        <button
          type="button"
          onClick={() => signOut.mutate()}
          disabled={signOut.isPending}
          className={btnSecondary}
        >
          <Trans>Log out</Trans>
        </button>
      </div>
    </header>
  )
}
