# vgkeep docs

The index of `docs/`. Each entry says what the reader finds there.

## Start here

- [architecture.md](architecture.md): the system view - context and container diagrams, the end-to-end flows,
  the datastore map, deployment topology, and the map of everything else.
- [../README.md](../README.md): bring-up, dev commands, ports, and repo layout.

## Operations

- [runbooks/README.md](runbooks/README.md): the runbook index, with the alert-to-runbook table.
- [runbooks/stack.md](runbooks/stack.md): the whole stack - topology, bring-up and teardown, ports, dashboards,
  alerting, telemetry pipeline operations, and the cross-service failure scenarios.
- One runbook per component: [auth](runbooks/auth.md), [bff](runbooks/bff.md), [collection](runbooks/collection.md),
  [enrichment](runbooks/enrichment.md), [social](runbooks/social.md), [user](runbooks/user.md), and the
  [frontend](runbooks/frontend.md).

## Production

- [production-paths.md](production-paths.md): the dev-to-production swap for each seam - datastores, secrets, SPA
  delivery, observability, edge, TLS, cluster operations, CI.

## Contributing guides

- [translations.md](translations.md): how to contribute a language.
- [adding-a-region.md](adding-a-region.md): the graduation checklist for a new entry region.

## Brand

- [brand/README.md](brand/README.md): the vgkeep mark, lockups, and how the PNG assets are rendered.
