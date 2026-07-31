import { site } from '../lib/site'

// Copy about the instance itself. Deployment facts (operator,
// contact, active sources) come from site(); everything else
// describes the software and holds for every instance.
const SOURCE_NOTES: Record<string, string> = {
  igdb: 'Titles, platforms, genres, release dates, and cover art come from the IGDB catalog. Cover images load from IGDB directly.',
  pricecharting:
    'Market values for loose, boxed, and sealed copies come from PriceCharting listings.',
  frankfurter:
    'Currency conversion uses the European Central Bank reference rates published by frankfurter.dev.',
}

export default function About() {
  const s = site()
  return (
    <main aria-label="About" className="mx-auto w-full max-w-2xl p-6">
      <h2 className="mb-4 text-2xl font-bold">About {s.name}</h2>
      <div className="flex flex-col gap-4 text-sm text-gray-700">
        <p>
          {s.name} is an instance of vgkeep, self-hosted software for tracking a video game
          collection. You catalog the games and hardware you own, organize them with tags and
          shelves, follow market prices, and share shelves with other people on this instance.
        </p>
        <p>
          This instance is run by {s.operator || 'the operator of this instance'}.
          {s.contact && (
            <>
              {' '}
              Contact:{' '}
              <a className="underline hover:text-gray-900" href={`mailto:${s.contact}`}>
                {s.contact}
              </a>
              .
            </>
          )}
        </p>
        <section aria-label="Source code">
          <h3 className="mb-1 font-semibold text-gray-900">Source code</h3>
          <p>
            vgkeep is free software under the AGPL-3.0 license. The source for this instance is
            available at{' '}
            <a className="underline hover:text-gray-900" href={s.sourceUrl}>
              {s.sourceUrl}
            </a>
            .
          </p>
        </section>
        {s.dataSources.length > 0 && (
          <section aria-label="Data sources">
            <h3 className="mb-1 font-semibold text-gray-900">Data sources</h3>
            <ul className="flex flex-col gap-2">
              {s.dataSources.map((d) => (
                <li key={d.key}>
                  {d.dataType} provided by{' '}
                  <a className="underline hover:text-gray-900" href={d.url}>
                    {d.label}
                  </a>
                  . {SOURCE_NOTES[d.key]}
                </li>
              ))}
            </ul>
          </section>
        )}
      </div>
    </main>
  )
}
