# vg-collect Bruno collection

API flows against the Tilt dev stack, using the `local` environment. Two
styles of folder live here:

- `bff/` talks to the public APISIX gateway (`bff_url`, localhost:8090)
  the way a browser does, authenticating with a session cookie out of
  Bruno's cookie jar.
- `auth/` and `user/` talk straight to the services (`auth_url` on
  localhost:8082, `user_url` on localhost:8081; both are Tilt
  port-forwards) using Bearer tokens.
- `enrichment/` talks straight to the enrichment service
  (`enrichment_url`, localhost:8084, also a Tilt port-forward) using
  Bearer tokens, the same bootstrap as `auth/` and `user/`: run `auth /
  dev token` first.
- `collection/` talks straight to the collection service
  (`collection_url`, localhost:8085, a Tilt port-forward) using
  Bearer tokens.

## Quick start

1. `task run` (Tilt) and wait for green.
2. Select the `local` environment in Bruno.
3. Run `auth / dev token (fixture login)`. It stores `access_token`,
   `refresh_token`, and `user_id` into the environment; every other
   request in `auth/` and `user/` reads them from there.
4. Run `user / get self`, then the refresh/reuse/revoke flows in order
   to watch rotation and reuse detection happen.

## Account linking flows (auth/ + user/)

Logins resolve identity-first: a `(provider, subject)` pair that is
linked to an account signs into that account even when its email
differs, and identities never silently move between accounts. The
`auth/` folder walks the surface with Bearer tokens (run `dev token`
first):

1. `dev link (bob joins this account)` binds the dev-bob identity to
   the current token's account (idempotent 200; 409
   `identity_already_linked` when dev-bob already belongs elsewhere).
2. `identities (linked logins)` lists the account's logins and stores
   the bob row's id.
3. `unlink identity (bob leaves)` removes it (second run 404; the last
   remaining login answers 409 `last_identity`).
4. `delete user auth (wipe logins + sessions)` is the auth leg of
   account deletion: 204, idempotent, SESSION-ENDING (refresh dies;
   log in again with `dev token`). The user row and collection data
   survive, and the next login re-binds via the verified email.

`user / update self` edits the profile (self only; validation answers
400 naming the field). There is deliberately no direct delete-user
flow: deleting the row alone would orphan collection data, so the
orchestrated deletion lives in `bff / delete me`.

## BFF cookie flows

The `bff/` folder exercises the same edge the SPA uses: the public
gateway on `bff_url` (localhost:8090), with the session carried in
Bruno's cookie jar rather than an `Authorization` header. Run them in
sequence with the `local` environment selected:

1. `providers` lists the enabled login providers (the dev stack offers
   `dev`).
2. `dev login (populates the cookie jar)` hits
   `/api/auth/login?provider=dev&user=alice`. The gateway completes the
   dev login and returns the session as a `Set-Cookie`; Bruno's cookie
   jar captures it. Run this once.
3. `me (cookie-authenticated)` returns the alice fixture's profile. No
   token wiring is needed: the jar replays the session cookie
   automatically.
4. The collection relays (`collection entries`, `recommendations`,
   `collection value history`) exercise the domain surface through the
   same cookie, and `fx rates` relays the enrichment exchange-rate
   snapshot (target-units-per-USD, USD omitted) the SPA converts market
   values with client-side; a cold fx cache answers 502 and the app just
   renders USD.
5. The account-management flows mirror the SPA's account page:
   `me update` edits the profile, including `preferred_currency` (pattern
   `^[A-Z]{3}$`) that drives the SPA's display conversion (edits survive
   later logins - provider claims fill the profile only at creation);
   `link dev bob` is the
   linking navigation (302 to `/account?linked=dev`, or
   `?link_error=conflict` when dev-bob already belongs to another
   account); `me identities` lists the linked logins and stores the bob
   row's id; `unlink bob` removes it (409 `last_identity` guards the
   only remaining login).
6. The proxy-pricing path prices an entry from any PriceCharting
   listing through the gateway: `search pc listings (via gateway)`
   searches all of PriceCharting with per-listing prices, `resolve pc
   listing (via gateway)` mints (or finds) the listing-backed product
   and stores its id, and `create proxy entry (priced by a pc
   listing)` creates an entry whose `value_cents` comes from that
   listing.
7. `search games (via gateway)` stores an igdb game and one of its platforms, and
   `resolve game with manual match (via gateway)` resolves it carrying
   the stored pc listing: game identity is listing-keyed, so the
   resolve lands on the product for exactly that listing (confidence
   1, verified false), minting it once and converging on every re-run.
   Auto-match stays the default for picks without a listing.
8. Teardown, in order: `delete me` deletes the whole account in
   self-healing order (collection purge, auth wipe, user row, session
   teardown) - fixture accounts are disposable, the next dev login
   recreates one fresh. `logout (clears the cookie, revokes the
   chain)` ends the session and revokes its refresh chain server-side;
   the jar drops the cleared cookie. After this, `me` returns 401
   until you log in again. It is idempotent, so it is also safe to run
   right after `delete me` already ended the session.

## Fixture users only

The dev token endpoint resolves fixture handles only: `alice`, `bob`,
`admin`. It can never mint for a real account; that is a deliberate
property of the dev provider, not a missing feature.

The `admin` fixture starts with the plain `user` role. The user service
has no role-grant endpoint (roles are data it owns), so granting admin
is a manual, visible step:

    kubectl -n vg-collect exec statefulset/user-pg -- \
      psql -U user -d user -c \
      "INSERT INTO user_roles (user_id, role) \
       SELECT id, 'admin' FROM users WHERE email = 'admin@example.com' \
       ON CONFLICT DO NOTHING;"

Roles land in the JWT at the next login or refresh.

Enrichment's two `admin -` requests need this same grant: run the
`psql` insert above, then run `auth / dev token` again as `admin` (a
fresh login is what puts the role in the JWT) before `admin - trigger
refresh walk` and `admin - correct product mapping` will pass their
role check. Before the grant, or with `alice`'s or `bob`'s token, both
answer 403.

This holds for both dev paths: `bff/`'s `dev login` and `auth/`'s `dev
token` resolve the same fixture handles and never a real account.

## Enrichment flows

The `enrichment/` folder exercises the catalog and pricing surface
directly against `enrichment_url` (localhost:8084). With the `local`
environment selected, run `auth / dev token` once, then the folder in
`seq` order:

1. `search games` and `search hardware` query the fixture catalog and
   need no product to exist yet.
2. `resolve game (find-or-create, auto-matched)` and `resolve game
   (deliberately unmatched fixture)` must run before `get product`,
   `prices batch (matched + unmatched)`, and `admin - correct product
   mapping`: the two resolve requests store `product_id` and
   `unmatched_product_id` into the environment, and those three later
   requests read them from there.
3. `resolve hardware (console)` and `recommendations score` round out
   the surface and have no ordering dependency beyond the initial
   token.
4. `search pc listings (all of pricecharting)` searches every
   PriceCharting listing with per-listing prices (degrading to the
   local catalog if the provider errors), and `resolve pc listing
   (price anchor)` mints the listing-backed product any entry can
   proxy its price from.
5. `admin - trigger refresh walk` and `admin - correct product mapping`
   need the admin role grant described above.

`price history batch` returns a product's snapshot series (default 90
days, oldest first) and, like `get product` and `prices batch`, needs a
resolved `product_id` first; a just-resolved product already answers
with its resolve-time point.

Every request in this folder works credential-less: stub mode serves
search, resolve, pricing, and recommendations entirely from the fixture
IGDB/PriceCharting catalogs baked into the service, so no real provider
keys are needed to run the folder end to end.

## Collection flows

`collection/` talks straight to the collection service
(`collection_url`, localhost:8085, a Tilt port-forward) with Bearer
tokens. Bootstrap chain: `auth / dev token` (token), then
`enrichment / resolve game` (product_id), then `collection / create
entry`. The flows are numbered in a happy-path order: two entries,
list + backlog order, a reorder, a tag, a pin/rate update, a single
entry read, a tag list, a saved
view, the dashboard, the library summary, and a custom off-catalog
entry priced by proxy. Reruns are mostly idempotent; tag and view
creations answer 409 on the second run (names are unique per user).

`create entry (custom price)` and `update entry (custom price)` both
carry `custom_value_cents` (the USD snapshot the backend aggregates
with) and the entered pair (`custom_value_entered_cents` plus
`custom_value_entered_currency`, the amount the user actually typed,
echoed back for pinned display); send both together or neither, and
only alongside `custom_value_cents`.

`value history` plots the collection's worth over the last ninety days,
one point per snapshot day; it is cached about five minutes and
invalidated by your own entry mutations.

`purge user data` (last of the standard entry flows) deletes everything
the token's user owns - entries, tags, saved views - in one idempotent
204 and drops the cached dashboard. It is the collection leg of account
deletion and doubles as cleanup: run it to wipe the debris this folder
created before a fresh run.

`resnapshot release dates` is an operator one-shot, not part of the
happy path: it POSTs to the manually-mounted `/internal/resnapshot`
(behind the same JWT middleware, so run `auth / dev token` first) and
recomputes every game-backed entry's release date from its product's
per-region dates. Run it ONCE after the release-dates deploy has healed
the products (enrichment's refresh walk repairs their per-region
dates), and only then; it is idempotent, so a second run reports
`entries_updated: 0`. Expect 200 with
`products_seen`/`products_failed`/`entries_updated`.

Through the gateway, `bff/collection entries`, `bff/collection value
history`, and `bff/recommendations` exercise the same domain with the
session cookie instead of a Bearer header; the value-history relay is
uncached (the collection service owns the composition and its cache).

## Testing as a real user

Real accounts authenticate through a real provider login, which the dev
provider cannot mint. The browser-facing gateway makes this reachable:
log in fresh in a browser (an incognito window works well), move the
session cookie into Bruno's cookie jar, and stop using that browser
session so only Bruno drives it from then on.

Do NOT paste a cookie value into a static `Cookie:` header. The bff
transparently rotates the refresh token and re-seals the cookie on
responses (`Set-Cookie`); a stale static header eventually replays a
consumed refresh token, and reuse detection then treats it as theft and
revokes the whole session chain. That is correct behavior, but
surprising mid-testing. Bruno's cookie jar honors `Set-Cookie` and stays
in step, so use it instead.
