package server

import (
	"encoding/json"
	"errors"
	"net/http"
	"sort"
	"strings"

	"github.com/google/uuid"
	openapi_types "github.com/oapi-codegen/runtime/types"

	"github.com/levonn-dev/vgkeep/libs/go/contract/common"
	"github.com/levonn-dev/vgkeep/services/collection/internal/gen/api"
	"github.com/levonn-dev/vgkeep/services/collection/internal/store"
)

// The /shared handlers serve any authenticated caller and never scope to the
// caller's sub: the shelf OWNER is the execution subject, the caller only
// reads. Visibility gates here cover the shelf only; the bff composes the owner-profile half.

const coverStripLimit = 4

// filtersFromViewParams tolerantly parses the frontend's stored view
// vocabulary into Filters + groupBy: unknown keys and invalid enums drop
// (matching the SPA's parse); region has no enum to drop against and rides
// through verbatim; mode is frontend-only; sort "value" falls to orderClause's stable default.
func filtersFromViewParams(params []byte) (store.Filters, string) {
	var doc struct {
		ItemType      []string `json:"item_type"`
		Status        []string `json:"status"`
		Packaging     []string `json:"packaging"`
		Region        []string `json:"region"`
		Developer     []string `json:"developer"`
		Publisher     []string `json:"publisher"`
		ItemCondition []string `json:"item_condition"`
		PlatformID    []int64  `json:"platform_id"`
		TagID         []string `json:"tag_id"`
		Sort          string   `json:"sort"`
		Order         string   `json:"order"`
		GroupBy       string   `json:"group_by"`
	}
	f := store.Filters{Sort: "created_at", Order: "desc"}
	if err := json.Unmarshal(params, &doc); err != nil {
		return f, ""
	}
	keep := func(vals []string, valid func(string) bool) []string {
		out := []string{}
		for _, v := range vals {
			if valid(v) {
				out = append(out, v)
			}
		}
		return out
	}
	f.ItemTypes = keep(doc.ItemType, validItemType)
	f.Statuses = keep(doc.Status, validStatus)
	f.Packagings = keep(doc.Packaging, validPackaging)
	// region has no allowed set to gate against (open-world); a stored free-text
	// value passes through exactly like the live list endpoint. Credits share the posture.
	f.Regions = doc.Region
	f.Developers = doc.Developer
	f.Publishers = doc.Publisher
	f.ItemConditions = keep(doc.ItemCondition, validCondition)
	f.PlatformIDs = doc.PlatformID
	for _, raw := range doc.TagID {
		if id, err := uuid.Parse(raw); err == nil {
			f.TagIDs = append(f.TagIDs, id)
		}
	}
	if api.ListEntriesParamsSort(doc.Sort).Valid() {
		f.Sort = doc.Sort
	}
	if api.ListEntriesParamsOrder(doc.Order).Valid() {
		f.Order = doc.Order
	}
	groupBy := ""
	if api.ListEntriesParamsGroupBy(doc.GroupBy).Valid() {
		groupBy = doc.GroupBy
	}
	return f, groupBy
}

func toSharedShelf(v store.View) (api.SharedShelf, error) {
	var params map[string]any
	if err := json.Unmarshal(v.Params, &params); err != nil {
		return api.SharedShelf{}, err
	}
	return api.SharedShelf{
		Id: v.ID, Name: v.Name, Slug: v.Slug, OwnerId: v.UserID,
		Visibility:  api.SharedShelfVisibility(v.Visibility),
		PublishedAt: v.PublishedAt,
		Params:      params,
	}, nil
}

// toSharedEntry is the whitelist projection; every field here is deliberate
// (TestSharedEntryWhitelist pins the contract side). Per-field conversions
// mirror toAPIEntry's exactly, substituting SharedEntry's generated enum types.
func toSharedEntry(e store.Entry) common.SharedEntry {
	out := common.SharedEntry{
		Id:                    e.ID,
		ProductId:             e.ProductID,
		ItemType:              common.ItemType(e.ItemType),
		MediaType:             common.MediaType(e.MediaType),
		DisplayName:           e.DisplayName,
		CoverUrl:              e.CoverURL,
		LocalizedName:         e.LocalizedName,
		LocalizedNameTranslit: e.LocalizedNameTranslit,
		LocalizedCoverUrl:     e.LocalizedCoverURL,
		IgdbGameId:            e.IGDBGameID,
		Region:                e.Region,
		Edition:               e.Edition,
		Packaging:             common.Packaging(e.Packaging),
		HasBox:                e.HasBox,
		HasManual:             e.HasManual,
		BoxCondition:          (*common.ItemCondition)(e.BoxCondition),
		ManualCondition:       (*common.ItemCondition)(e.ManualCondition),
		ItemCondition:         (*common.ItemCondition)(e.ItemCondition),
		Pinned:                e.Pinned,
		CreatedAt:             e.CreatedAt,
	}
	if len(e.Developers) > 0 {
		out.Developers = &e.Developers
	}
	if len(e.Publishers) > 0 {
		out.Publishers = &e.Publishers
	}
	if e.PlatformName != nil {
		out.Platform = &common.EntryPlatform{IgdbPlatformId: e.PlatformIGDBID, Name: *e.PlatformName}
	}
	if e.FirstReleaseDate != nil {
		out.FirstReleaseDate = &openapi_types.Date{Time: *e.FirstReleaseDate}
	}
	tags := make([]common.TagRef, len(e.Tags))
	for i, t := range e.Tags {
		tags[i] = common.TagRef{Id: t.ID, Name: t.Name}
	}
	out.Tags = tags
	return out
}

// sharedShelfOr404 loads and gates a shelf; unknown and private are the same 404 (no existence oracle).
func (h *Handlers) sharedShelfOr404(w http.ResponseWriter, r *http.Request, id uuid.UUID) (store.View, bool) {
	v, err := h.store.GetSharedShelf(r.Context(), id)
	if errors.Is(err, store.ErrNotFound) || (err == nil && v.Visibility == "private") {
		problem(w, r, http.StatusNotFound, "shelf_not_found", "no such shelf")
		return store.View{}, false
	}
	if err != nil {
		h.internalError(w, r, "shelf_lookup", "shelf lookup failed", err)
		return store.View{}, false
	}
	return v, true
}

func (h *Handlers) GetSharedShelf(w http.ResponseWriter, r *http.Request, shelfId openapi_types.UUID) {
	v, ok := h.sharedShelfOr404(w, r, shelfId)
	if !ok {
		return
	}
	out, err := toSharedShelf(v)
	if err != nil {
		h.internalError(w, r, "shelf_encode", "shelf encoding failed", err)
		return
	}
	writeJSON(w, http.StatusOK, out)
}

func (h *Handlers) GetSharedShelfBySlug(w http.ResponseWriter, r *http.Request, params api.GetSharedShelfBySlugParams) {
	v, err := h.store.GetSharedShelfBySlug(r.Context(), params.OwnerId, store.NormalizeSlug(params.Slug))
	if errors.Is(err, store.ErrNotFound) || (err == nil && v.Visibility == "private") {
		problem(w, r, http.StatusNotFound, "shelf_not_found", "no such shelf")
		return
	}
	if err != nil {
		h.internalError(w, r, "shelf_lookup_by_slug", "shelf lookup failed", err)
		return
	}
	out, mErr := toSharedShelf(v)
	if mErr != nil {
		h.internalError(w, r, "shelf_encode", "shelf encoding failed", mErr)
		return
	}
	writeJSON(w, http.StatusOK, out)
}

func (h *Handlers) ListSharedShelfEntries(w http.ResponseWriter, r *http.Request, shelfId openapi_types.UUID, params api.ListSharedShelfEntriesParams) {
	// limit/offset are already known within bounds (specval enforces 1-200/>=0);
	// only the default-when-absent case needs handling here.
	limit := 100
	if params.Limit != nil {
		limit = *params.Limit
	}
	offset := 0
	if params.Offset != nil {
		offset = *params.Offset
	}
	v, ok := h.sharedShelfOr404(w, r, shelfId)
	if !ok {
		return
	}
	f, groupBy := filtersFromViewParams(v.Params)
	entries, err := h.store.ListEntries(r.Context(), v.UserID, f)
	if err != nil {
		h.internalError(w, r, "shared_shelf_entries_list", "list failed", err)
		return
	}
	total := len(entries)
	start := min(offset, total)
	page := entries[start:min(start+limit, total)]
	apiEntries := make([]common.SharedEntry, len(page))
	for i, e := range page {
		apiEntries[i] = toSharedEntry(e)
	}
	out := api.SharedEntryList{TotalCount: total}
	if groupBy == "" {
		out.Entries = &apiEntries
	} else {
		groups := buildSharedGroups(page, apiEntries, groupBy)
		out.Groups = &groups
	}
	writeJSON(w, http.StatusOK, out)
}

// ListSharedShelves pages listed shelves, optionally scoped to a caller-given
// owner set: absent/empty owner_ids means unfiltered (Explore-recent's read);
// present scopes to those owners (the profile page's read), via
// ListListedShelves' nil-slice contract. Bounds are specval's job (owner_ids
// maxItems 5000, limit/offset 1-100/>=0).
func (h *Handlers) ListSharedShelves(w http.ResponseWriter, r *http.Request, params api.ListSharedShelvesParams) {
	limit := 20
	if params.Limit != nil {
		limit = *params.Limit
	}
	offset := 0
	if params.Offset != nil {
		offset = *params.Offset
	}
	var owners []uuid.UUID
	if params.OwnerIds != nil {
		owners = make([]uuid.UUID, len(*params.OwnerIds))
		copy(owners, *params.OwnerIds)
	}
	views, total, err := h.store.ListListedShelves(r.Context(), owners, limit, offset)
	if err != nil {
		h.internalError(w, r, "shared_shelves_list", "list shelves failed", err)
		return
	}
	summaries, err := h.shelfSummaries(r, views)
	if err != nil {
		h.internalError(w, r, "shared_shelves_summaries", "summaries failed", err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"total_count": total, "shelves": summaries})
}

// GetSharedShelvesByIds batch-hydrates shelf summaries; ids' maxItems
// (contract: 100) is specval's job.
func (h *Handlers) GetSharedShelvesByIds(w http.ResponseWriter, r *http.Request, params api.GetSharedShelvesByIdsParams) {
	ids := make([]uuid.UUID, len(params.Ids))
	copy(ids, params.Ids)
	views, err := h.store.SharedShelvesByIDs(r.Context(), ids)
	if err != nil {
		h.internalError(w, r, "shared_shelves_by_ids", "shelves by ids failed", err)
		return
	}
	summaries, err := h.shelfSummaries(r, views)
	if err != nil {
		h.internalError(w, r, "shared_shelves_summaries", "summaries failed", err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"shelves": summaries})
}

// shelfSummaries composes card data per shelf: filtered entry count plus the
// first covers in shelf order; two small indexed queries per shelf, fine at ~20/page.
func (h *Handlers) shelfSummaries(r *http.Request, views []store.View) ([]api.SharedShelfSummary, error) {
	out := make([]api.SharedShelfSummary, 0, len(views))
	for _, v := range views {
		// Defense in depth: both store queries already exclude private
		// (ListListedShelves filters visibility='listed', SharedShelvesByIDs
		// excludes visibility<>'private'), but private must never reach output
		// even if a future path forgets to filter it.
		if v.Visibility == "private" {
			continue
		}
		f, _ := filtersFromViewParams(v.Params)
		count, err := h.store.CountEntriesFiltered(r.Context(), v.UserID, f)
		if err != nil {
			return nil, err
		}
		covers, err := h.store.CoverURLs(r.Context(), v.UserID, f, coverStripLimit)
		if err != nil {
			return nil, err
		}
		out = append(out, api.SharedShelfSummary{
			Id: v.ID, Name: v.Name, Slug: v.Slug, OwnerId: v.UserID,
			Visibility:  api.SharedShelfVisibility(v.Visibility),
			PublishedAt: v.PublishedAt, EntryCount: count, CoverUrls: covers,
		})
	}
	return out, nil
}

// buildSharedGroups mirrors buildGroups (entries handler) over SharedEntry:
// same partition/sort/catch-all-last rules, reusing groupLabels/catchAllLabels.
func buildSharedGroups(entries []store.Entry, apiEntries []common.SharedEntry, groupBy string) []common.SharedEntryGroup {
	byLabel := map[string][]common.SharedEntry{}
	for i, e := range entries {
		for _, label := range groupLabels(e, groupBy) {
			byLabel[label] = append(byLabel[label], apiEntries[i])
		}
	}
	labels := make([]string, 0, len(byLabel))
	for label := range byLabel {
		labels = append(labels, label)
	}
	sort.Slice(labels, func(i, j int) bool {
		ci, cj := catchAllLabels[labels[i]], catchAllLabels[labels[j]]
		if ci != cj {
			return cj // catch-all sorts last
		}
		return strings.ToLower(labels[i]) < strings.ToLower(labels[j])
	})
	groups := make([]common.SharedEntryGroup, len(labels))
	for i, label := range labels {
		groups[i] = common.SharedEntryGroup{Key: label, Label: label, Entries: byLabel[label]}
	}
	return groups
}
