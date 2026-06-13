# vg-collect Bruno collection

API flows against the Tilt dev stack, using the `local` environment. Two
styles of folder live here:

- `bff/` talks to the public APISIX gateway (`bff_url`, localhost:8090)
  the way a browser does, authenticating with a session cookie out of
  Bruno's cookie jar.
- `auth/` and `user/` talk straight to the services (`auth_url` on
  localhost:8082, `user_url` on localhost:8081; both are Tilt
  port-forwards) using Bearer tokens.

## Quick start

1. `task run` (Tilt) and wait for green.
2. Select the `local` environment in Bruno.
3. Run `auth / dev token (fixture login)`. It stores `access_token`,
   `refresh_token`, and `user_id` into the environment; every other
   request in `auth/` and `user/` reads them from there.
4. Run `user / get self`, then the refresh/reuse/revoke flows in order
   to watch rotation and reuse detection happen.

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
4. `logout (clears the cookie, revokes the chain)` ends the session and
   revokes its refresh chain server-side; the jar drops the cleared
   cookie. After this, `me` returns 401 until you log in again.

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

This holds for both dev paths: `bff/`'s `dev login` and `auth/`'s `dev
token` resolve the same fixture handles and never a real account.

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
