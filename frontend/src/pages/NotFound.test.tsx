import { render, screen } from '@testing-library/react'
import { MemoryRouter, Route, Routes } from 'react-router'
import NotFound from './NotFound'

it('renders the heading and a link home for an unknown path', () => {
  render(
    <MemoryRouter initialEntries={['/no-such-page']}>
      <Routes>
        <Route path="*" element={<NotFound />} />
      </Routes>
    </MemoryRouter>,
  )
  expect(screen.getByRole('heading', { name: 'Page not found' })).toBeInTheDocument()
  expect(screen.getByRole('link', { name: 'Go to the start page' })).toHaveAttribute('href', '/')
})
