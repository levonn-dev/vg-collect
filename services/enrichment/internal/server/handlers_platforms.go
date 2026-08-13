// The platform catalog: cached reads and the console-name reverse
// lookup hardware resolve and mapping use.

package server

import (
	"context"
	"encoding/json"
	"net/http"
	"sort"

	"github.com/levonn-dev/vgkeep/services/enrichment/internal/gen/api"
	"github.com/levonn-dev/vgkeep/services/enrichment/internal/match"
	"github.com/levonn-dev/vgkeep/services/enrichment/internal/store"
)

// ListPlatforms serves the platform catalog joined with alias
// knowledge, for the custom-entry picker and the normalize lever. The
// answer is cached 24h (the search-cache idiom): reference data that
// only changes when the platform sweep lands new rows, so a cold build
// fetches wholesale through ensurePlatforms and the rest read Valkey.
func (h *Handlers) ListPlatforms(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	if body, err := h.cache.GetPlatforms(ctx); err != nil {
		h.failOpen(ctx, "platforms_get", err)
	} else if body != nil {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write(body)
		return
	}
	cats, err := h.ensurePlatforms(ctx)
	if err != nil {
		problem(w, r, http.StatusBadGateway, "upstream_unavailable", "the platform catalog is unavailable")
		return
	}
	out := api.PlatformCatalog{Platforms: make([]api.CatalogPlatform, 0, len(cats))}
	for _, c := range cats {
		// PlatformAliases returns nil for a platform with no known
		// aliases; the contract types aliases a required string[], so
		// coalesce to [] rather than marshal a null the picker cannot
		// filter over.
		al := match.PlatformAliases(c.Name)
		if al == nil {
			al = []string{}
		}
		out.Platforms = append(out.Platforms, api.CatalogPlatform{
			IgdbId: c.ID, Name: c.Name, Aliases: al,
		})
	}
	sort.Slice(out.Platforms, func(i, j int) bool { return out.Platforms[i].Name < out.Platforms[j].Name })
	body, err := json.Marshal(out)
	if err != nil {
		h.internalError(w, r, "platforms_encode", "encoding failed", err)
		return
	}
	if err := h.cache.PutPlatforms(ctx, body, h.searchTTL); err != nil {
		h.failOpen(ctx, "platforms_put", err)
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(body)
}

// ensurePlatforms serves the cached IGDB platform catalog, fetching it
// wholesale on first need or after the staleness horizon (stale serves
// when the provider is down).
func (h *Handlers) ensurePlatforms(ctx context.Context) ([]store.CatalogPlatform, error) {
	at, err := h.store.PlatformsFetchedAt(ctx)
	if err != nil {
		return nil, err
	}
	if !at.IsZero() && h.now().Sub(at) < h.igdbRefreshAfter {
		return h.store.ListPlatforms(ctx)
	}
	ps, err := h.games.Platforms(ctx)
	if err != nil {
		if !at.IsZero() {
			h.logger.WarnContext(ctx, "platform refetch failed; serving stale", "err", err)
			return h.store.ListPlatforms(ctx)
		}
		return nil, err
	}
	if err := h.store.UpsertPlatforms(ctx, ps, h.now()); err != nil {
		h.logger.WarnContext(ctx, "platform upsert failed", "err", err)
	}
	rows := make([]store.CatalogPlatform, 0, len(ps))
	for _, p := range ps {
		rows = append(rows, store.CatalogPlatform{
			ID: p.ID, Name: p.Name, Abbreviation: p.Abbreviation,
			Generation: p.Generation, LogoURL: p.LogoURL(),
		})
	}
	return rows, nil
}

// platformFor reverse-maps a PriceCharting console-name onto the IGDB
// platform catalog; nil when nothing maps.
func (h *Handlers) platformFor(ctx context.Context, consoleName string) *store.Platform {
	ps, err := h.ensurePlatforms(ctx)
	if err != nil {
		h.logger.WarnContext(ctx, "platform catalog unavailable; hardware keeps no platform ref", "err", err)
		return nil
	}
	for _, p := range ps {
		if match.ConsoleMatches(p.Name, consoleName, "") {
			return &store.Platform{IGDBID: p.ID, Name: p.Name, LogoURL: p.LogoURL}
		}
	}
	return nil
}

// platformLogoFor reads the catalog logo for a platform id ("" when
// the catalog is unavailable or the platform ships no logo).
func (h *Handlers) platformLogoFor(ctx context.Context, igdbID int64) string {
	ps, err := h.ensurePlatforms(ctx)
	if err != nil {
		h.logger.WarnContext(ctx, "platform catalog unavailable; product keeps no platform logo", "err", err)
		return ""
	}
	for _, p := range ps {
		if p.ID == igdbID {
			return p.LogoURL
		}
	}
	return ""
}
