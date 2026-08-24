import { useState } from 'react'
import { fireEvent, screen } from '@testing-library/react'
import { renderWithI18n } from '../test/i18n'
import StringListInput from './StringListInput'

// A controlled host: StringListInput takes values/onChange as props, so
// exercising a real remove-then-remove sequence needs something that
// actually re-renders with the updated array between clicks, same as
// every real caller (CustomStep, ReviewPanel).
function ControlledList({ initial }: { initial: string[] }) {
  const [values, setValues] = useState(initial)
  return <StringListInput label="Developers" addLabel="Add developer" values={values} onChange={setValues} />
}

test('renders one input per value and edits the right row', () => {
  const onChange = vi.fn()
  renderWithI18n(
    <StringListInput label="Developers" addLabel="Add developer" values={['Garage Team', 'Second Studio']} onChange={onChange} />,
  )
  const first = screen.getByLabelText('Developers: 1')
  expect(first).toHaveValue('Garage Team')
  fireEvent.change(screen.getByLabelText('Developers: 2'), { target: { value: 'Third Studio' } })
  expect(onChange).toHaveBeenLastCalledWith(['Garage Team', 'Third Studio'])
})

test('the add button appends an empty row', () => {
  const onChange = vi.fn()
  renderWithI18n(<StringListInput label="Developers" addLabel="Add developer" values={[]} onChange={onChange} />)
  fireEvent.click(screen.getByRole('button', { name: 'Add developer' }))
  expect(onChange).toHaveBeenLastCalledWith([''])
})

test('remove deletes its own row', () => {
  const onChange = vi.fn()
  renderWithI18n(
    <StringListInput label="Developers" addLabel="Add developer" values={['A', 'B']} onChange={onChange} />,
  )
  fireEvent.click(screen.getByRole('button', { name: 'Remove Developers 1' }))
  expect(onChange).toHaveBeenLastCalledWith(['B'])
})

test('removing two rows via references captured before either removal targets the intended rows, not position-shifted ones', () => {
  renderWithI18n(<ControlledList initial={['A', 'B', 'C']} />)
  // Captured once, up front - the "no re-query" scenario a position
  // (index) key would get wrong: a stable id must keep each button
  // bound to its own row even after an earlier row is gone.
  const [removeFirst, removeSecond] = screen.getAllByRole('button', { name: /^Remove/ })
  fireEvent.click(removeFirst)
  fireEvent.click(removeSecond)
  expect(screen.getByLabelText('Developers: 1')).toHaveValue('C')
  expect(screen.queryByLabelText('Developers: 2')).not.toBeInTheDocument()
})

test('the add button hides at the ten-name cap', () => {
  const ten = Array.from({ length: 10 }, (_, i) => `Studio ${i}`)
  renderWithI18n(<StringListInput label="Developers" addLabel="Add developer" values={ten} onChange={() => {}} />)
  expect(screen.queryByRole('button', { name: 'Add developer' })).not.toBeInTheDocument()
})
