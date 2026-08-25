import { useState } from 'react'
import { screen } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { renderWithI18n } from '../test/i18n'
import StringListInput from './StringListInput'

// StringListInput takes values/onChange as props; a real remove-then-remove
// sequence needs a host that re-renders with the updated array, like real callers.
function ControlledList({ initial, spy }: { initial: string[]; spy?: (v: string[]) => void }) {
  const [values, setValues] = useState(initial)
  return (
    <StringListInput
      label="Developers"
      addLabel="Add developer"
      values={values}
      onChange={(v) => {
        spy?.(v)
        setValues(v)
      }}
    />
  )
}

test('renders one input per value and edits the right row', async () => {
  const onChange = vi.fn()
  renderWithI18n(<ControlledList initial={['Garage Team', 'Second Studio']} spy={onChange} />)
  const first = screen.getByLabelText('Developers: 1')
  expect(first).toHaveValue('Garage Team')
  const second = screen.getByLabelText('Developers: 2')
  await userEvent.clear(second)
  await userEvent.type(second, 'Third Studio')
  expect(onChange).toHaveBeenLastCalledWith(['Garage Team', 'Third Studio'])
})

test('the add button appends an empty row', async () => {
  const onChange = vi.fn()
  renderWithI18n(<StringListInput label="Developers" addLabel="Add developer" values={[]} onChange={onChange} />)
  await userEvent.click(screen.getByRole('button', { name: 'Add developer' }))
  expect(onChange).toHaveBeenLastCalledWith([''])
})

test('remove deletes its own row', async () => {
  const onChange = vi.fn()
  renderWithI18n(
    <StringListInput label="Developers" addLabel="Add developer" values={['A', 'B']} onChange={onChange} />,
  )
  await userEvent.click(screen.getByRole('button', { name: 'Remove Developers 1' }))
  expect(onChange).toHaveBeenLastCalledWith(['B'])
})

test('removing two rows via references captured before either removal targets the intended rows, not position-shifted ones', async () => {
  renderWithI18n(<ControlledList initial={['A', 'B', 'C']} />)
  // Captured once, up front: a position (index) key would rebind this button
  // after an earlier row is gone; a stable id must not.
  const [removeFirst, removeSecond] = screen.getAllByRole('button', { name: /^Remove/ })
  await userEvent.click(removeFirst)
  await userEvent.click(removeSecond)
  expect(screen.getByLabelText('Developers: 1')).toHaveValue('C')
  expect(screen.queryByLabelText('Developers: 2')).not.toBeInTheDocument()
})

test('the add button hides at the ten-name cap', () => {
  const ten = Array.from({ length: 10 }, (_, i) => `Studio ${i}`)
  renderWithI18n(<StringListInput label="Developers" addLabel="Add developer" values={ten} onChange={() => {}} />)
  expect(screen.queryByRole('button', { name: 'Add developer' })).not.toBeInTheDocument()
})
