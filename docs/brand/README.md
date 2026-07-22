# Brand assets

The vg-collect mark: a pixel "VG" monogram, white letters knocked out
of an indigo (#4F46E5) rounded tile. Identical in light and dark
themes.

Sources (edit these):

- logo.svg: wide tile (4:3), the site-header and lockup proportion.
- logo-square.svg: square tile, the favicon and app-icon proportion.
- lockup.svg: wide mark plus the "vg-collect" wordmark in the bold
  system sans stack; the wordmark uses currentColor so the surface
  sets its ink.

Generated (never edit by hand, rerun the script instead):

- icon-512.png, lockup-light.png, lockup-dark.png (in this directory;
  lockups render at 4x for promo use).
- favicon-32.png and apple-touch-icon.png in frontend/public/.

frontend/public/favicon.svg is a byte-for-byte copy of logo-square.svg;
keep them in sync when the mark changes.

Regenerate the PNGs:

    node docs/brand/render.mjs

Requires the frontend npm deps and the playwright chromium browser
(cd frontend && npm ci && npx playwright install chromium).
