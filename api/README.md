# api/

OpenAPI contracts for the vgkeep services, plus the domain tables.

## Layout

- `auth.yaml`, `bff.yaml`, `collection.yaml`, `enrichment.yaml`, `social.yaml`,
  `user.yaml` - the authored service contracts, one per service.
- `common.yaml` - the authored shared component library: every schema that is
  part of more than one service's wire contract, plus the bearer security
  scheme. It has no paths of its own; the service contracts reference it with
  relative `$ref`s (`./common.yaml#/components/...`).
- `bundled/` - generated: a self-contained copy of each service contract with
  every `common.yaml` reference inlined, produced by redocly via
  `task gen:bundle` (part of `task gen`). Never hand-edit these; they are
  committed so a consumer can pick up a single-file contract without running
  the bundler, and CI fails on drift.
- `domain.yaml` - region/platform domain tables consumed by domaingen
  (`task gen:domain`); not an OpenAPI document.

## Editing flow

1. Edit the authored contract, and `common.yaml` when the change touches a
   shared component. A schema belongs in `common.yaml` exactly when two or
   more service contracts carry it; each former copy site holds a `$ref` to
   the common entry. The same rule applies to enum value sets: a set that
   appears at two or more sites is a named vocabulary schema (`EntryStatus`,
   `ItemType`, ...) - in `common.yaml` when its sites span contracts, in its
   own contract otherwise - and every property or parameter carrying the set
   references it. A site that keeps a per-site `description` or `default`
   beside the reference wraps it as `allOf: [$ref]` with the extra keys as
   siblings (a bare `$ref` drops sibling keys in every consumer). Enums that
   appear at exactly one site stay inline.
2. Run `task gen` to rebundle `bundled/` and regenerate code, then commit the
   authored change together with the regenerated output.
