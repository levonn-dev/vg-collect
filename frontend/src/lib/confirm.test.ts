import { confirmThen } from './confirm'

afterEach(() => {
  vi.restoreAllMocks()
})

it('runs the callback when the dialog is accepted', () => {
  vi.spyOn(window, 'confirm').mockReturnValue(true)
  const run = vi.fn()
  confirmThen('Delete this entry?', run)
  expect(window.confirm).toHaveBeenCalledWith('Delete this entry?')
  expect(run).toHaveBeenCalledTimes(1)
})

it('does not run the callback when the dialog is declined', () => {
  vi.spyOn(window, 'confirm').mockReturnValue(false)
  const run = vi.fn()
  confirmThen('Delete this entry?', run)
  expect(run).not.toHaveBeenCalled()
})
