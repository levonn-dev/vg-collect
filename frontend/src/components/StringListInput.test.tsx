import { fireEvent, screen } from '@testing-library/react'
import { renderWithI18n } from '../test/i18n'
import StringListInput from './StringListInput'

test('renders one input per value and edits the right row', () => {
  const onChange = vi.fn()
  renderWithI18n(
    <StringListInput label="Developers" addLabel="Add developer" values={['Garage Team', 'Second Studio']} onChange={onChange} />,
  )
  const first = screen.getByLabelText('Developers 1')
  expect(first).toHaveValue('Garage Team')
  fireEvent.change(screen.getByLabelText('Developers 2'), { target: { value: 'Third Studio' } })
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

test('the add button hides at the ten-name cap', () => {
  const ten = Array.from({ length: 10 }, (_, i) => `Studio ${i}`)
  renderWithI18n(<StringListInput label="Developers" addLabel="Add developer" values={ten} onChange={() => {}} />)
  expect(screen.queryByRole('button', { name: 'Add developer' })).not.toBeInTheDocument()
})
