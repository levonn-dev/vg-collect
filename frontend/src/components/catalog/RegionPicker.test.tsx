import { fireEvent, screen } from '@testing-library/react'
import { renderWithI18n } from '../../test/i18n'
import RegionPicker from './RegionPicker'

test('known value renders the labeled select', () => {
  renderWithI18n(<RegionPicker value="ntsc_j" onChange={() => {}} />)
  expect(screen.getByLabelText('Region')).toHaveValue('ntsc_j')
  expect(screen.getByRole('option', { name: 'NTSC-J' })).toBeInTheDocument()
})

test('escape hatch flips to free text and back', () => {
  const onChange = vi.fn()
  renderWithI18n(<RegionPicker value="" onChange={onChange} />)
  fireEvent.click(screen.getByRole('button', { name: "My region isn't listed" }))
  fireEvent.change(screen.getByLabelText('Region'), { target: { value: 'Korea' } })
  expect(onChange).toHaveBeenLastCalledWith('Korea')
  fireEvent.click(screen.getByRole('button', { name: 'Pick a known region instead' }))
  expect(onChange).toHaveBeenLastCalledWith('')
})

test('free-text value opens in text mode', () => {
  renderWithI18n(<RegionPicker value="Korea" onChange={() => {}} />)
  expect(screen.getByLabelText('Region')).toHaveValue('Korea')
  expect(screen.getByRole('button', { name: 'Pick a known region instead' })).toBeInTheDocument()
})

test('graduated regions are known select values with labels', () => {
  renderWithI18n(<RegionPicker value="korea" onChange={() => {}} />)
  expect(screen.getByLabelText('Region')).toHaveValue('korea')
  expect(screen.getByRole('option', { name: 'Korea' })).toBeInTheDocument()
  expect(screen.getByRole('option', { name: 'Brazil' })).toBeInTheDocument()
  expect(screen.getByRole('option', { name: 'China' })).toBeInTheDocument()
})

test('regionGroup renders both optgroups', () => {
  renderWithI18n(
    <RegionPicker value="ntsc_j" onChange={() => {}} regionGroup={{ platformName: 'Super Famicom', regions: ['ntsc_j'] }} />,
  )
  expect(screen.getByRole('group', { name: 'Released on Super Famicom' })).toBeInTheDocument()
  expect(screen.getByRole('group', { name: 'Other regions' })).toBeInTheDocument()
})

test('required renders on the select and, after the escape hatch, on the input', () => {
  renderWithI18n(<RegionPicker value="ntsc_j" onChange={() => {}} required />)
  expect(screen.getByLabelText('Region')).toBeRequired()
  fireEvent.click(screen.getByRole('button', { name: "My region isn't listed" }))
  expect(screen.getByLabelText('Region')).toBeRequired()
})

test('required is absent by default', () => {
  renderWithI18n(<RegionPicker value="ntsc_j" onChange={() => {}} />)
  expect(screen.getByLabelText('Region')).not.toBeRequired()
})

test('placeholder option exists and selecting a value fires onChange', () => {
  const onChange = vi.fn()
  renderWithI18n(<RegionPicker value="" onChange={onChange} />)
  const select = screen.getByLabelText('Region')
  expect(screen.getByRole('option', { name: 'Choose...' })).toBeInTheDocument()
  expect(select).toHaveValue('')
  fireEvent.change(select, { target: { value: 'ntsc_u' } })
  expect(onChange).toHaveBeenCalledWith('ntsc_u')
})
