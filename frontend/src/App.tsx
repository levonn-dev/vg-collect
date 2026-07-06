import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { BrowserRouter, Route, Routes } from 'react-router'
import { ApiError } from './api/client'
import Layout from './components/Layout'
import AddWizard from './pages/AddWizard'
import Collection from './pages/Collection'
import Dashboard from './pages/Dashboard'
import EntryDetail from './pages/EntryDetail'
import Login from './pages/Login'
import Recommendations from './pages/Recommendations'

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
            <Route path="/" element={<Collection />} />
            <Route path="/add" element={<AddWizard />} />
            <Route path="/dashboard" element={<Dashboard />} />
            <Route path="/entries/:id" element={<EntryDetail />} />
            <Route path="/recommendations" element={<Recommendations />} />
          </Route>
        </Routes>
      </BrowserRouter>
    </QueryClientProvider>
  )
}
