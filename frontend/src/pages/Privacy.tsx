import { site } from '../lib/site'

// Privacy statements here must track actual app behavior: what the
// stores keep, what deletion removes, and which third parties see
// browser traffic. Not legal advice; operators deploying publicly
// should have it reviewed. Move the Last updated date on change.
export default function Privacy() {
  const s = site()
  const providers =
    s.authProviders.map((p) => p.label).join(' or ') || 'your sign-in provider'
  const igdbActive = s.dataSources.some((d) => d.key === 'igdb')
  const contact = s.contact || 'the operator of this instance'
  return (
    <main aria-label="Privacy policy" className="mx-auto w-full max-w-2xl p-6">
      <h2 className="mb-4 text-2xl font-bold">Privacy policy</h2>
      <div className="flex flex-col gap-4 text-sm text-gray-700">
        <section aria-label="What is stored">
          <h3 className="mb-1 font-semibold text-gray-900">What is stored</h3>
          <p>
            Your account: when you sign in with {providers}, {s.name} stores your email address,
            your display name, and a link to your avatar image. The avatar itself loads from the
            sign-in provider's servers.
          </p>
          <p className="mt-2">
            Everything you enter through the site: collection entries, tags, shelves, catalog
            submissions, comments, follows, likes, and profile settings.
          </p>
        </section>
        <section aria-label="Cookies">
          <h3 className="mb-1 font-semibold text-gray-900">Cookies</h3>
          <p>
            {s.name} sets one cookie, __Host-vg_session, which keeps you signed in. It is
            encrypted, unreadable to scripts, and expires with your session. There are no
            analytics, advertising, or tracking cookies.
          </p>
        </section>
        <section aria-label="Third parties">
          <h3 className="mb-1 font-semibold text-gray-900">Third parties</h3>
          {igdbActive && (
            <p>
              Cover art loads in your browser straight from IGDB's servers, which see your IP
              address the way any image host does.
            </p>
          )}
          <p className={igdbActive ? 'mt-2' : undefined}>
            Your avatar loads from your sign-in provider's servers the same way. Price and
            exchange-rate data are fetched by the server; those providers see no traffic from your
            browser.
          </p>
        </section>
        <section aria-label="Deleting your account">
          <h3 className="mb-1 font-semibold text-gray-900">Deleting your account</h3>
          <p>
            Deleting your account on the Account page removes your collection, tags, shelves,
            linked logins, and profile. Comments you left on other people's shelves lose their
            text and author name; a "Deleted user" placeholder remains. Contributions approved
            into the shared catalog stay in the shared catalog and store no link to your account.
          </p>
        </section>
        <section aria-label="Changes and contact">
          <h3 className="mb-1 font-semibold text-gray-900">Changes and contact</h3>
          <p>
            The operator may change this policy; the date below moves when that happens. Questions
            about your data go to {contact}.
          </p>
        </section>
        <p className="text-xs text-gray-500">Last updated: 2026-07-30</p>
      </div>
    </main>
  )
}
