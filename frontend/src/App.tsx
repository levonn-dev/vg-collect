import { lazy, Suspense } from 'react'
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { BrowserRouter, Route, Routes } from 'react-router'
import { ApiError } from './api/client'
import Layout from './components/Layout'
import PublicShell from './components/PublicShell'
import About from './pages/About'
import Account from './pages/Account'
import AddWizard from './pages/AddWizard'
import Collection from './pages/Collection'
import EntryDetail from './pages/EntryDetail'
import Explore from './pages/Explore'
import Feed from './pages/Feed'
import Help from './pages/Help'
import Home from './pages/Home'
import Login from './pages/Login'
import NotFound from './pages/NotFound'
import Privacy from './pages/Privacy'
import Profile from './pages/Profile'
import Recommendations from './pages/Recommendations'
import SharedShelf from './pages/SharedShelf'
import Terms from './pages/Terms'

// Admin is the one route regular users never visit, and its panels are
// a fifth of the page code - code-split so they ride their own chunk.
// The null fallback is a sub-perceptible blank between click and
// chunk; a spinner string here would also drag a new msgid through
// both catalogs for a moderator-only flash.
const Admin = lazy(() => import('./pages/Admin'))

const queryClient = new QueryClient({
  defaultOptions: {
    queries: {
      // /api/me and the provider list change rarely; a non-zero default
      // stops a refetch on every window focus across all pages.
      staleTime: 5 * 60 * 1000,
      // 401 means "go log in", not "retry harder".
      retry: (failureCount, error) =>
        !(error instanceof ApiError && error.status === 401) && failureCount < 2,
    },
  },
})

export default function App() {
  return (
    <QueryClientProvider client={queryClient}>
      <BrowserRouter>
        <Routes>
          <Route element={<PublicShell />}>
            <Route path="/login" element={<Login />} />
            <Route path="/about" element={<About />} />
            <Route path="/terms" element={<Terms />} />
            <Route path="/privacy" element={<Privacy />} />
            <Route path="*" element={<NotFound />} />
          </Route>
          <Route element={<Layout />}>
            <Route path="/" element={<Home />} />
            <Route path="/collection" element={<Collection />} />
            <Route path="/add" element={<AddWizard />} />
            <Route path="/entries/:id" element={<EntryDetail />} />
            <Route path="/recommendations" element={<Recommendations />} />
            <Route path="/explore" element={<Explore />} />
            <Route path="/u/:handle" element={<Profile />} />
            <Route path="/u/:handle/shelves/:slug" element={<SharedShelf />} />
            <Route path="/feed" element={<Feed />} />
            <Route
              path="/admin"
              element={
                <Suspense fallback={null}>
                  <Admin />
                </Suspense>
              }
            />
            <Route path="/account" element={<Account />} />
            <Route path="/help" element={<Help />} />
          </Route>
        </Routes>
      </BrowserRouter>
    </QueryClientProvider>
  )
}
