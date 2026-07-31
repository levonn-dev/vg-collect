import { site } from '../lib/site'

// Boilerplate terms for a self-hosted instance, not legal advice.
// Operators deploying publicly should have this reviewed. Statements
// about deletion and content must track actual app behavior; change
// the Last updated date when the text changes.
export default function Terms() {
  const s = site()
  const providers =
    s.authProviders.map((p) => p.label).join(' or ') || 'a third-party sign-in provider'
  const contact = s.contact || 'the operator of this instance'
  return (
    <main aria-label="Terms of service" className="mx-auto w-full max-w-2xl p-6">
      <h2 className="mb-4 text-2xl font-bold">Terms of service</h2>
      <div className="flex flex-col gap-4 text-sm text-gray-700">
        <section aria-label="Acceptance">
          <h3 className="mb-1 font-semibold text-gray-900">Acceptance</h3>
          <p>
            These terms govern your use of {s.name}. By creating an account or using the site you
            accept them.
          </p>
        </section>
        <section aria-label="Accounts">
          <h3 className="mb-1 font-semibold text-gray-900">Accounts</h3>
          <p>
            Signing in requires an account with {providers}. You are responsible for what happens
            under your account, and you can delete it at any time from the Account page.
          </p>
        </section>
        <section aria-label="Your content">
          <h3 className="mb-1 font-semibold text-gray-900">Your content</h3>
          <p>
            What you enter stays yours: collection entries, tags, shelves, catalog submissions,
            comments, and profile fields. You grant the operator the right to store and display
            that content as the site's features require, such as showing a comment to people
            viewing the shelf it was left on.
          </p>
          <p className="mt-2">
            Deleting your account removes your collection, tags, shelves, linked logins, and
            profile. Comments you left on other people's shelves lose their text and author name; a
            "Deleted user" placeholder remains. Contributions approved into the shared catalog stay
            there. They store no link to your account.
          </p>
        </section>
        <section aria-label="Acceptable use">
          <h3 className="mb-1 font-semibold text-gray-900">Acceptable use</h3>
          <p>
            Do not use {s.name} to harass people, to post unlawful content, to disrupt the
            service, or to scrape it at volume. The operator may remove content or close accounts
            that do.
          </p>
        </section>
        <section aria-label="Termination">
          <h3 className="mb-1 font-semibold text-gray-900">Termination</h3>
          <p>
            You can stop using {s.name} at any time and delete your account from the Account
            page. The operator may suspend or close accounts that break these terms, and may
            stop running the service: this is self-hosted software, and an instance lives only
            as long as its operator runs it.
          </p>
        </section>
        <section aria-label="Warranty and liability">
          <h3 className="mb-1 font-semibold text-gray-900">Warranty and liability</h3>
          <p>
            {s.name} is provided as is, without warranty of any kind. Market values and exchange
            rates are estimates built from third-party data; they can be wrong or out of date. To
            the extent the law allows, the operator is not liable for damage arising from use of
            the site, including data loss or decisions based on displayed prices.
          </p>
        </section>
        <section aria-label="Changes">
          <h3 className="mb-1 font-semibold text-gray-900">Changes</h3>
          <p>
            The operator may change these terms. The date below moves when that happens; continued
            use after a change means acceptance.
          </p>
        </section>
        <section aria-label="Governing law">
          <h3 className="mb-1 font-semibold text-gray-900">Governing law</h3>
          <p>
            These terms are governed by the law of {s.jurisdiction || "the operator's jurisdiction"}
            .
          </p>
        </section>
        <section aria-label="Contact">
          <h3 className="mb-1 font-semibold text-gray-900">Contact</h3>
          <p>Questions about these terms go to {contact}.</p>
        </section>
        <p className="text-xs text-gray-500">Last updated: 2026-07-30</p>
      </div>
    </main>
  )
}
