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

// The /shared handlers serve any authenticated caller. They never
// scope to the caller's sub: the OWNER of the shelf is the execution
// subject, the caller only reads. Visibility gates here cover the
// shelf only; the bff composes in the owner-profile half of the
// effective-visibility rule.

const coverStripLimit = 4

// filtersFromViewParams tolerantly parses the frontend's stored view
// vocabulary ({v:1, item_type, status, packaging, region,
// item_condition, platform_id, tag_id, sort, order, group_by, mode})
// into Filters + groupBy. Unknown keys and invalid enum values are
// dropped, matching the SPA's own tolerant parse; region has no enum
// to drop against (open-world) and rides through verbatim. mode is
// frontend-only. A stored sort the list machinery cannot serve
// cheaply ("value") passes through to orderClause's stable default.
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
	// region has no allowed set to gate against (open-world); a
	// stored free-text value passes through exactly like the live
	// list endpoint's own filter param. Credits share the posture.
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

// toSharedEntry is the whitelist projection. Every field named here
// is deliberate; TestSharedEntryWhitelist pins the contract side. The
// per-field conversions mirror toAPIEntry's exactly (same
// expressions, substituting SharedEntry's generated enum types) -
// no new conversion helpers.
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

// sharedShelfOr404 loads and gates a shelf; unknown and private are
// the same 404 (no existence oracle).
func (h *Handlers) sharedShelfOr404(w http.ResponseWriter, r *http.Request, id uuid.UUID) (store.View, bool) {
	v, err := h.store.GetSharedShelf(r.Context(), id)
	if errors.Is(err, store.ErrNotFound) || (err == nil && v.Visibility == "private") {
		problem(w, r, http.StatusNotFound, "shelf_not_found", "no such shelf")
		return store.View{}, false
	}
	if err != nil {
		h.internalError(w, r, "shelf lookup failed", err)
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
		h.internalError(w, r, "shelf encoding failed", err)
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
		h.internalError(w, r, "shelf lookup failed", err)
		return
	}
	out, mErr := toSharedShelf(v)
	if mErr != nil {
		h.internalError(w, r, "shelf encoding failed", mErr)
		return
	}
	writeJSON(w, http.StatusOK, out)
}

func (h *Handlers) ListSharedShelfEntries(w http.ResponseWriter, r *http.Request, shelfId openapi_types.UUID, params api.ListSharedShelfEntriesParams) {
	// limit/offset are already known within bounds (specval enforces
	// the contract's 1-200/>=0) by the time this runs; only the
	// default-when-absent case needs handling here.
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
		h.internalError(w, r, "list failed", err)
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

// ListSharedShelves pages listed shelves, optionally scoped to a
// caller-given owner set. owner_ids absent or empty means unfiltered
// (Explore-recent's read, every listed owner); present, it scopes the
// page to just those owners (the profile page's read). Either way
// owners is nil when owner_ids is absent, so store.ListListedShelves'
// own nil-slice contract (nil = no filter) does the rest. owner_ids'
// maxItems and limit/offset's bounds are specval's job (contract
// maxItems: 5000 and 1-100/>=0 respectively); only the
// default-when-absent case needs handling here.
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
		h.internalError(w, r, "list shelves failed", err)
		return
	}
	summaries, err := h.shelfSummaries(r, views)
	if err != nil {
		h.internalError(w, r, "summaries failed", err)
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
		h.internalError(w, r, "shelves by ids failed", err)
		return
	}
	summaries, err := h.shelfSummaries(r, views)
	if err != nil {
		h.internalError(w, r, "summaries failed", err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"shelves": summaries})
}

// shelfSummaries composes card data per shelf: filtered entry count
// plus the first covers in shelf order. Two small indexed queries per
// shelf on a page of at most ~20 - fine at this tier; revisit only
// with measurements.
func (h *Handlers) shelfSummaries(r *http.Request, views []store.View) ([]api.SharedShelfSummary, error) {
	out := make([]api.SharedShelfSummary, 0, len(views))
	for _, v := range views {
		// Defense in depth: the store queries backing both callers
		// already exclude private (ListListedShelves filters
		// visibility='listed', SharedShelvesByIDs excludes
		// visibility<>'private'), but private must never reach shared
		// output even if some future store path forgets to filter it.
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

// buildSharedGroups mirrors buildGroups (entries handler) over the
// SharedEntry projection: same partition/sort/catch-all-last rules,
// reusing groupLabels and catchAllLabels since both read the store
// entries, not the API projection.
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
