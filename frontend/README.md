# vgkeep frontend

React 19 SPA for the vgkeep video game collection tracker.
Typed against the BFF OpenAPI contract at `api/bff.yaml`.
Served in production by the BFF at the same origin; in dev the Vite
proxy forwards `/api` to the APISIX gateway port-forward on :8090.

## Dev commands

    npm run dev          start the Vite dev server on :5173
    npm run test         vitest (unit, jsdom)
    npm run test:cover   vitest + 80% coverage gate
    npm run lint         eslint
    npm run build        tsc + vite build
    npm run gen          regenerate src/api/schema.d.ts from api/bff.yaml

See the root README for the full stack (task run / task down).
