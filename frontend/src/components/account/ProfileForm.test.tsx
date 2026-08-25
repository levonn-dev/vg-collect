import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { screen } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import type { Me } from '../../api/me'
import { jsonResponse, meFixture, problemResponse, requestPath } from '../../test/fixtures'
import { renderWithI18n } from '../../test/i18n'
import ProfileForm from './ProfileForm'

// Mocks the PATCH /api/me save call; me arrives as a prop, so there's
// nothing else for this component to fetch.
function stubFetch(response: Response = jsonResponse(200, meFixture())) {
  const fetchMock = vi.fn().mockResolvedValue(response)
  vi.stubGlobal('fetch', fetchMock)
  return fetchMock
}

function renderProfileForm(me: Me = meFixture()) {
  const qc = new QueryClient({ defaultOptions: { queries: { retry: false } } })
  return renderWithI18n(
    <QueryClientProvider client={qc}>
      <ProfileForm me={me} />
    </QueryClientProvider>,
  )
}

afterEach(() => vi.unstubAllGlobals())

it('renders profile fields seeded from /api/me and saves via PATCH', async () => {
  const fetchMock = stubFetch()
  renderProfileForm()
  const input = await screen.findByLabelText('Handle')
  expect(input).toHaveValue('Alice')
  await userEvent.clear(input)
  await userEvent.type(input, 'Alicia')
  await userEvent.click(screen.getByRole('button', { name: 'Save' }))
  expect(await screen.findByRole('status')).toHaveTextContent('Saved.')
  const patch = fetchMock.mock.calls.find((c) => (c[0] as Request).method === 'PATCH')
  expect(requestPath(patch?.[0])).toBe('/api/me')
  expect(await (patch?.[0] as Request).clone().text()).toBe(JSON.stringify({
    handle: 'Alicia', avatar_url: '', profile_visibility: 'private', landing_page: 'feed',
  }))
})

it('retracts the Saved. confirmation once the form is edited again', async () => {
  stubFetch()
  renderProfileForm()
  const input = await screen.findByLabelText('Handle')
  await userEvent.click(screen.getByRole('button', { name: 'Save' }))
  expect(await screen.findByRole('status')).toHaveTextContent('Saved.')
  await userEvent.type(input, 'x')
  expect(screen.queryByRole('status')).not.toBeInTheDocument()
})

it('carries client-side handle validation matching the server rules', async () => {
  renderProfileForm()
  const input = await screen.findByLabelText('Handle')
  expect(input).toHaveAttribute('minLength', '2')
  expect(input).toHaveAttribute('pattern', '[a-zA-Z0-9](?:[a-zA-Z0-9_]{0,28}[a-zA-Z0-9])?')
  expect(input).toHaveAttribute('title', '2-30 characters, letters/digits, underscores inside only')
})

it('submits the selected profile visibility alongside the rest of the form', async () => {
  const fetchMock = stubFetch()
  renderProfileForm()
  await userEvent.click(
    await screen.findByRole('radio', { name: 'Listed - appears in Explore and search' }),
  )
  await userEvent.click(screen.getByRole('button', { name: 'Save' }))
  expect(await screen.findByRole('status')).toHaveTextContent('Saved.')
  const patch = fetchMock.mock.calls.find((c) => (c[0] as Request).method === 'PATCH')
  expect(requestPath(patch?.[0])).toBe('/api/me')
  expect(await (patch?.[0] as Request).clone().text()).toBe(JSON.stringify({
    handle: 'Alice', avatar_url: '', profile_visibility: 'listed', landing_page: 'feed',
  }))
})

it('renders the default-page fieldset seeded from me.landing_page', async () => {
  renderProfileForm(meFixture({ landing_page: 'explore' }))
  expect(await screen.findByRole('radio', { name: 'Explore' })).toBeChecked()
  expect(screen.getByRole('radio', { name: 'Feed' })).not.toBeChecked()
  expect(screen.getByRole('radio', { name: 'Collection' })).not.toBeChecked()
  expect(screen.getByText('Where the app opens after you sign in.')).toBeInTheDocument()
})

it('submits the selected default page alongside the rest of the form', async () => {
  const fetchMock = stubFetch()
  renderProfileForm()
  await userEvent.click(await screen.findByRole('radio', { name: 'Collection' }))
  await userEvent.click(screen.getByRole('button', { name: 'Save' }))
  expect(await screen.findByRole('status')).toHaveTextContent('Saved.')
  const patch = fetchMock.mock.calls.find((c) => (c[0] as Request).method === 'PATCH')
  expect(requestPath(patch?.[0])).toBe('/api/me')
  expect(await (patch?.[0] as Request).clone().text()).toBe(JSON.stringify({
    handle: 'Alice', avatar_url: '', profile_visibility: 'private', landing_page: 'collection',
  }))
})

it('shows a specific message when the handle is taken', async () => {
  stubFetch(problemResponse(409, 'handle_taken'))
  renderProfileForm()
  await userEvent.click(await screen.findByRole('button', { name: 'Save' }))
  expect(await screen.findByRole('alert')).toHaveTextContent('That handle is taken.')
})

it('shows a specific message when the handle cooldown blocks the change', async () => {
  stubFetch(problemResponse(429, 'handle_cooldown'))
  renderProfileForm()
  await userEvent.click(await screen.findByRole('button', { name: 'Save' }))
  expect(await screen.findByRole('alert'))
    .toHaveTextContent('Handle changed too recently - try again later.')
})

it('hides the copy-link button while the profile is private', async () => {
  renderProfileForm()
  await screen.findByLabelText('Handle')
  expect(screen.queryByRole('button', { name: 'Copy profile link' })).not.toBeInTheDocument()
})

it('copies the profile link once visibility is not private', async () => {
  const writeText = vi.fn().mockResolvedValue(undefined)
  Object.defineProperty(navigator, 'clipboard', { value: { writeText }, configurable: true })
  renderProfileForm(meFixture({ profile_visibility: 'listed' }))
  await userEvent.click(await screen.findByRole('button', { name: 'Copy profile link' }))
  expect(writeText).toHaveBeenCalledWith(`${window.location.origin}/u/Alice`)
})
