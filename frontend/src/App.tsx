import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { BrowserRouter, Route, Routes } from 'react-router'
import { ApiError } from './api/client'
import Layout from './components/Layout'
import Account from './pages/Account'
import AddWizard from './pages/AddWizard'
import Admin from './pages/Admin'
import Collection from './pages/Collection'
import EntryDetail from './pages/EntryDetail'
import Explore from './pages/Explore'
import Feed from './pages/Feed'
import Home from './pages/Home'
import Login from './pages/Login'
import Profile from './pages/Profile'
import Recommendations from './pages/Recommendations'
import SharedShelf from './pages/SharedShelf'

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
          <Route path="/login" element={<Login />} />
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
            <Route path="/admin" element={<Admin />} />
            <Route path="/account" element={<Account />} />
          </Route>
        </Routes>
      </BrowserRouter>
    </QueryClientProvider>
  )
}
