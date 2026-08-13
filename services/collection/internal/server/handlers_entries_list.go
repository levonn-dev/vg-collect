// Entry list pipeline: query-param validation, in-memory value
// sorting, grouping, and pagination for the list endpoint.

package server

import (
	"net/http"
	"sort"
	"strings"

	"github.com/google/uuid"

	"github.com/levonn-dev/vgkeep/libs/go/httpkit"
	"github.com/levonn-dev/vgkeep/services/collection/internal/gen/api"
	"github.com/levonn-dev/vgkeep/services/collection/internal/store"
)

// listParams validates and converts the generated query params (the
// generated layer binds but does not enforce enum membership or
// ranges). Returns filters, groupBy, limit, offset, and the 400
// detail (empty means valid).
func listParams(params api.ListEntriesParams) (store.Filters, string, int, int, string) {
	f := store.Filters{Sort: "created_at", Order: "desc"}
	if params.ItemType != nil {
		for _, v := range *params.ItemType {
			if !v.Valid() {
				return f, "", 0, 0, "item_type contains an unknown value"
			}
			f.ItemTypes = append(f.ItemTypes, string(v))
		}
	}
	if params.Status != nil {
		for _, v := range *params.Status {
			if !v.Valid() {
				return f, "", 0, 0, "status contains an unknown value"
			}
			f.Statuses = append(f.Statuses, string(v))
		}
	}
	if params.Packaging != nil {
		for _, v := range *params.Packaging {
			if !v.Valid() {
				return f, "", 0, 0, "packaging contains an unknown value"
			}
			f.Packagings = append(f.Packagings, string(v))
		}
	}
	if params.Region != nil {
		for _, v := range *params.Region {
			f.Regions = append(f.Regions, string(v))
		}
	}
	// Credits are open-world snapshot facts (IGDB and community names
	// alike): no allowed set to gate against, same as region.
	if params.Developer != nil {
		f.Developers = *params.Developer
	}
	if params.Publisher != nil {
		f.Publishers = *params.Publisher
	}
	if params.ItemCondition != nil {
		for _, v := range *params.ItemCondition {
			if !v.Valid() {
				return f, "", 0, 0, "item_condition contains an unknown value"
			}
			f.ItemConditions = append(f.ItemConditions, string(v))
		}
	}
	if params.PlatformId != nil {
		f.PlatformIDs = *params.PlatformId
	}
	if params.TagId != nil {
		f.TagIDs = *params.TagId
	}
	if params.Sort != nil {
		if !params.Sort.Valid() {
			return f, "", 0, 0, "sort is not a known value"
		}
		f.Sort = string(*params.Sort)
	}
	if params.Order != nil {
		if !params.Order.Valid() {
			return f, "", 0, 0, "order must be asc or desc"
		}
		f.Order = string(*params.Order)
	}
	groupBy := ""
	if params.GroupBy != nil {
		if !params.GroupBy.Valid() {
			return f, "", 0, 0, "group_by is not a known value"
		}
		groupBy = string(*params.GroupBy)
	}
	limit, ok := httpkit.ClampOrReject(params.Limit, 200, 1, 500)
	if !ok {
		return f, "", 0, 0, "limit must be between 1 and 500"
	}
	offset, ok := httpkit.ClampOrReject(params.Offset, 0, 0)
	if !ok {
		return f, "", 0, 0, "offset must not be negative"
	}
	return f, groupBy, limit, offset, ""
}

// sortEntriesByValue re-sorts in memory after price composition:
// pinned first, then value with nulls last, then the standard
// tiebreak. Stable, so equal keys keep the SQL base order.
func sortEntriesByValue(entries []store.Entry, values map[uuid.UUID]*int64, order string) {
	sort.SliceStable(entries, func(i, j int) bool {
		if entries[i].Pinned != entries[j].Pinned {
			return entries[i].Pinned
		}
		vi, vj := values[entries[i].ID], values[entries[j].ID]
		switch {
		case vi == nil && vj == nil:
			// fall through to the tiebreak
		case vi == nil:
			return false // nulls last in both directions
		case vj == nil:
			return true
		case *vi != *vj:
			if order == "desc" {
				return *vi > *vj
			}
			return *vi < *vj
		}
		if !entries[i].CreatedAt.Equal(entries[j].CreatedAt) {
			return entries[i].CreatedAt.After(entries[j].CreatedAt)
		}
		return entries[i].ID.String() < entries[j].ID.String()
	})
}

// groupLabels names the group(s) an entry belongs to; group_by=tag
// repeats a multi-tagged entry in each of its tag groups.
func groupLabels(e store.Entry, groupBy string) []string {
	switch groupBy {
	case "platform":
		if e.PlatformName != nil {
			return []string{*e.PlatformName}
		}
		return []string{"Unknown"}
	case "status":
		return []string{e.Status}
	case "item_type":
		return []string{e.ItemType}
	case "location":
		if e.StorageLocation != nil && *e.StorageLocation != "" {
			return []string{*e.StorageLocation}
		}
		return []string{"Unassigned"}
	default: // tag
		if len(e.Tags) == 0 {
			return []string{"Untagged"}
		}
		labels := make([]string, len(e.Tags))
		for i, t := range e.Tags {
			labels[i] = t.Name
		}
		return labels
	}
}

var catchAllLabels = map[string]bool{"Unknown": true, "Unassigned": true, "Untagged": true}

// buildGroups partitions the sorted entries, preserving order within
// each group; groups sort by label ascending with the catch-all last.
func buildGroups(entries []store.Entry, apiEntries []api.Entry, groupBy string) []api.EntryGroup {
	byLabel := map[string][]api.Entry{}
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
	groups := make([]api.EntryGroup, len(labels))
	for i, label := range labels {
		groups[i] = api.EntryGroup{Key: label, Label: label, Entries: byLabel[label]}
	}
	return groups
}

// ListEntries answers one page of the filter x sort x group matrix.
// The full filtered set is fetched and sorted (person-scale by
// design: pagination bounds payloads, not queries), total_count is
// taken, then the page sliced. Prices arrive in one batched call -
// over every effective id when sorting by value (the order needs them
// all), otherwise over the page only. Enrichment being down degrades
// to pricing_available=false, never a failure.
func (h *Handlers) ListEntries(w http.ResponseWriter, r *http.Request, params api.ListEntriesParams) {
	userID, bearer, ok := h.caller(w, r)
	if !ok {
		return
	}
	f, groupBy, limit, offset, detail := listParams(params)
	if detail != "" {
		problem(w, r, http.StatusBadRequest, "invalid_param", detail)
		return
	}
	entries, err := h.store.ListEntries(r.Context(), userID, f)
	if err != nil {
		h.internalError(w, r, "list failed", err)
		return
	}

	pricingAvailable := true
	values := map[uuid.UUID]*int64{}
	compose := func(subset []store.Entry) {
		var ids []uuid.UUID
		for _, e := range subset {
			if e.PricingMode == "custom" {
				values[e.ID] = e.CustomValueCents
				continue
			}
			if id := effectiveProductID(e.PricingMode, e.ProductID, e.PricingProductID); id != nil {
				ids = append(ids, *id)
			}
		}
		if len(ids) == 0 {
			return
		}
		prices, err := h.enrichment.BatchPrices(r.Context(), bearer, ids)
		h.composeEvent(r.Context(), "list", err)
		if err != nil {
			pricingAvailable = false
			h.logger.WarnContext(r.Context(), "list value composition unavailable", "err", err)
			return
		}
		for _, e := range subset {
			if e.PricingMode == "custom" {
				continue
			}
			if id := effectiveProductID(e.PricingMode, e.ProductID, e.PricingProductID); id != nil {
				if p, okPrice := prices[id.String()]; okPrice {
					values[e.ID] = valueForPackaging(e.Packaging, p)
				}
			}
		}
	}
	if f.Sort == "value" {
		compose(entries)
		sortEntriesByValue(entries, values, f.Order)
	}

	total := len(entries)
	// offset has no contract upper bound; clamp into range before adding
	// limit so the sum can never overflow.
	start := min(offset, total)
	page := entries[start:min(start+limit, total)]
	if f.Sort != "value" {
		compose(page)
	}

	apiEntries := make([]api.Entry, len(page))
	for i, e := range page {
		apiEntries[i] = toAPIEntry(e, values[e.ID])
	}
	out := api.EntryList{PricingAvailable: pricingAvailable, TotalCount: total}
	if groupBy == "" {
		out.Entries = &apiEntries
	} else {
		groups := buildGroups(page, apiEntries, groupBy)
		out.Groups = &groups
	}
	writeJSON(w, http.StatusOK, out)
}
