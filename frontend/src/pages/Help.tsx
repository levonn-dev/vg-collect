// UI labels quoted here must match the rendered app; when a label
// changes, this copy changes in the same round. Deployment facts
// (which providers run) live on the About page, not here.
export default function Help() {
  return (
    <main aria-label="Help" className="mx-auto w-full max-w-2xl p-6">
      <h2 className="mb-4 text-2xl font-bold">Help</h2>
      <div className="flex flex-col gap-6 text-sm text-gray-700">
        <section aria-labelledby="shelves-from-tags">
          <h3 id="shelves-from-tags" className="mb-1 font-semibold text-gray-900">
            Shelves from tags
          </h3>
          <p>
            A shelf is a saved view of your collection, so it can hold any items you pick, not
            just what one filter matches. The trick is a tag:
          </p>
          <ol className="mt-2 flex list-decimal flex-col gap-1 pl-5">
            <li>
              On the Collection page's Items tab, switch to the table view and press "Bulk edit".
            </li>
            <li>
              Check the items you want, then use "Add tags" in the bar that appears and apply a
              new tag, for example trade-pile.
            </li>
            <li>
              Open the filters and check that tag under "Tags (all of)". The list now shows
              exactly the items you picked.
            </li>
            <li>
              Press "Save shelf..." in the shelf picker and name it. The shelf keeps the tag
              filter, so tagging another item later adds it to the shelf too.
            </li>
          </ol>
        </section>
        <section aria-labelledby="visibility">
          <h3 id="visibility" className="mb-1 font-semibold text-gray-900">
            Who can see your shelves
          </h3>
          <p>
            Shelves and your profile each have one of three settings: Private (only you),
            Unlisted (anyone signed in who has your link), Listed (appears in Explore and search).
            Both gates apply. A listed shelf on a private profile is not visible to others; on
            an unlisted profile, the shelf is reachable by link but stays out of Explore. The Shelves tab
            shows a notice when your settings combine in these ways.
          </p>
        </section>
        <section aria-labelledby="currencies">
          <h3 id="currencies" className="mb-1 font-semibold text-gray-900">
            Currency display
          </h3>
          <p>
            The currency selector in the header changes how market values are shown. Market
            values are stored in USD and converted at European Central Bank reference rates; the
            pricing panel names the rate date. Price paid is different: it stays in the currency
            you entered and is never converted.
          </p>
        </section>
        <section aria-labelledby="adding">
          <h3 id="adding" className="mb-1 font-semibold text-gray-900">
            Adding games and hardware
          </h3>
          <p>
            Add searches the shared catalog first: pick games or hardware, search, choose a
            result. The details step records your copy: region, packaging, condition, price paid,
            and, for games, a "Match manually" control if the automatic price-listing match picks
            the wrong listing. Nothing in the catalog? "Can not find it? Add it as a custom item."
            creates a custom entry, and its entry page offers "Submit to catalog" to propose it
            for the shared catalog.
          </p>
        </section>
        <section aria-labelledby="prices">
          <h3 id="prices" className="mb-1 font-semibold text-gray-900">
            Where market prices come from
          </h3>
          <p>
            An item gets its market value by matching to a price listing; the confirm step shows
            the match and lets you change it. Values follow the packaging you set: loose, boxed,
            or sealed. Custom items start without pricing until you pick a similar listed item as
            their price source. The About page lists which price and exchange-rate providers this
            instance runs.
          </p>
        </section>
      </div>
    </main>
  )
}
