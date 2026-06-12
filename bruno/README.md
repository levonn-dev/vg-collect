# vg-collect Bruno collection

API flows against the Tilt dev stack, using the `local` environment
(auth on localhost:8082, user on localhost:8081; both are Tilt
port-forwards).

## Quick start

1. `task run` (Tilt) and wait for green.
2. Select the `local` environment in Bruno.
3. Run `auth / dev token (fixture login)`. It stores `access_token`,
   `refresh_token`, and `user_id` into the environment; every other
   request reads them from there.
4. Run `user / get self`, then the refresh/reuse/revoke flows in order
   to watch rotation and reuse detection happen.

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

## Testing as a real user

Real accounts authenticate through a real provider login. Once the
browser-facing gateway exists, the flow is: log in fresh in a browser
(an incognito window works well), copy the session cookie into Bruno's
cookie jar, and stop using that browser session.

Do NOT paste a cookie value into a static header. Refresh rotation
re-seals the cookie on responses (`Set-Cookie`); a static copied header
keeps replaying the consumed refresh token, which reuse detection
treats as theft and revokes the whole session chain. Bruno's cookie jar
honors `Set-Cookie` and stays in step.
