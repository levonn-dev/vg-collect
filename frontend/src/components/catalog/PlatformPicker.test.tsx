import { useState } from 'react'
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { screen } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { renderWithI18n } from '../../test/i18n'
import PlatformPicker from './PlatformPicker'
import type { PlatformValue } from './PlatformPicker'

const catalog = {
  platforms: [
    { igdb_id: 19, name: 'Super Nintendo Entertainment System', aliases: ['snes', 'super nintendo'] },
    { igdb_id: 18, name: 'Nintendo Entertainment System', aliases: ['nes'] },
  ],
}

function renderPicker(value: PlatformValue = { platformName: '' }) {
  const qc = new QueryClient({ defaultOptions: { queries: { retry: false } } })
  qc.setQueryData(['platforms'], catalog)
  const onChange = vi.fn()
  renderWithI18n(
    <QueryClientProvider client={qc}>
      <PlatformPicker value={value} onChange={onChange} />
    </QueryClientProvider>,
  )
  return onChange
}

// Harness wires PlatformPicker as a real controlled component (value
// state feeds back from onChange), so a test can drive a pick and then
// observe the confirmed-state re-render. renderPicker's static value
// prop cannot show this: nothing ever supplies it a new value.
function Harness({ initial, spy }: { initial: PlatformValue; spy: (v: PlatformValue) => void }) {
  const [value, setValue] = useState(initial)
  return (
    <PlatformPicker
      value={value}
      onChange={(v) => {
        spy(v)
        setValue(v)
      }}
    />
  )
}

function renderHarness(initial: PlatformValue = { platformName: '' }) {
  const qc = new QueryClient({ defaultOptions: { queries: { retry: false } } })
  qc.setQueryData(['platforms'], catalog)
  const spy = vi.fn()
  renderWithI18n(
    <QueryClientProvider client={qc}>
      <Harness initial={initial} spy={spy} />
    </QueryClientProvider>,
  )
  return spy
}

it('filters by alias and emits the canonical id + name on pick', async () => {
  const onChange = renderPicker()
  await userEvent.type(screen.getByLabelText(/platform/i), 'snes')
  // Alias match surfaces the canonical row as a pick button.
  await userEvent.click(await screen.findByRole('button', { name: /Super Nintendo Entertainment System/ }))
  expect(onChange).toHaveBeenCalledWith({ platformIgdbId: 19, platformName: 'Super Nintendo Entertainment System' })
})

it('the escape hatch stores free text with no id', async () => {
  const onChange = renderPicker()
  await userEvent.click(screen.getByRole('button', { name: /my platform isn't listed/i }))
  await userEvent.type(screen.getByLabelText(/platform/i), 'My Homebrew Rig')
  expect(onChange).toHaveBeenLastCalledWith({ platformIgdbId: undefined, platformName: 'My Homebrew Rig' })
})

it('a pick enters the confirmed state: input and suggestions gone, canonical name shown, Change present', async () => {
  renderHarness()
  await userEvent.type(screen.getByLabelText(/^platform$/i), 'snes')
  await userEvent.click(await screen.findByRole('button', { name: /Super Nintendo Entertainment System/ }))

  expect(screen.queryByLabelText(/^platform$/i)).not.toBeInTheDocument()
  expect(screen.queryByRole('button', { name: /Nintendo Entertainment System/ })).not.toBeInTheDocument()
  expect(screen.getByText('Super Nintendo Entertainment System')).toBeInTheDocument()
  expect(screen.getByRole('button', { name: 'Change platform' })).toHaveTextContent('Change')
})

it('Change clears the pick and returns an empty, editable input with live suggestions', async () => {
  const spy = renderHarness()
  await userEvent.type(screen.getByLabelText(/^platform$/i), 'snes')
  await userEvent.click(await screen.findByRole('button', { name: /Super Nintendo Entertainment System/ }))
  spy.mockClear()

  await userEvent.click(screen.getByRole('button', { name: 'Change platform' }))

  expect(spy).toHaveBeenCalledWith({ platformIgdbId: undefined, platformName: '' })
  expect(screen.queryByText('Super Nintendo Entertainment System')).not.toBeInTheDocument()
  const input = screen.getByLabelText(/^platform$/i)
  expect(input).toHaveValue('')
  await userEvent.type(input, 'snes')
  expect(await screen.findByRole('button', { name: /Super Nintendo Entertainment System/ })).toBeInTheDocument()
})

it('mounts confirmed immediately when the value prop already carries a canonical platform', () => {
  renderHarness({ platformIgdbId: 19, platformName: 'Super Nintendo Entertainment System' })
  expect(screen.getByText('Super Nintendo Entertainment System')).toBeInTheDocument()
  expect(screen.getByRole('button', { name: 'Change platform' })).toBeInTheDocument()
  expect(screen.queryByLabelText(/^platform$/i)).not.toBeInTheDocument()
})
