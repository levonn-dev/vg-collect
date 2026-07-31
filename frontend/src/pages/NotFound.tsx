import { Link } from 'react-router'

export default function NotFound() {
  return (
    <main aria-label="Page not found" className="p-6">
      <h2 className="mb-2 text-2xl font-bold">Page not found</h2>
      <p className="text-sm text-gray-600">
        There is no page at this address.{' '}
        <Link to="/" className="underline hover:text-gray-900">
          Go to the start page
        </Link>
        .
      </p>
    </main>
  )
}
