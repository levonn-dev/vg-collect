// The collection surface: entries, tags, saved views, and the
// dashboard, relayed to the collection service.

package server

import (
	"encoding/json"
	"net/http"

	"github.com/google/uuid"
	openapi_types "github.com/oapi-codegen/runtime/types"

	"github.com/levonn-dev/vgkeep/services/bff/internal/collectionclient"
	"github.com/levonn-dev/vgkeep/services/bff/internal/gen/api"
	"github.com/levonn-dev/vgkeep/services/bff/internal/gen/collectionapi"
)

func castSlice[T ~string, U ~string](in *[]T) *[]U {
	if in == nil {
		return nil
	}
	out := make([]U, len(*in))
	for i, v := range *in {
		out[i] = U(v)
	}
	return &out
}

func castVal[T ~string, U ~string](in *T) *U {
	if in == nil {
		return nil
	}
	u := U(*in)
	return &u
}

// collectionListParams re-types the mirrored query params for the
// generated collection client (the two contracts are byte-identical;
// only the Go package differs).
func collectionListParams(p api.ListEntriesParams) *collectionapi.ListEntriesParams {
	return &collectionapi.ListEntriesParams{
		ItemType:      castSlice[api.ListEntriesParamsItemType, collectionapi.ListEntriesParamsItemType](p.ItemType),
		Status:        castSlice[api.ListEntriesParamsStatus, collectionapi.ListEntriesParamsStatus](p.Status),
		Packaging:     castSlice[api.ListEntriesParamsPackaging, collectionapi.ListEntriesParamsPackaging](p.Packaging),
		Region:        p.Region,
		ItemCondition: castSlice[api.ListEntriesParamsItemCondition, collectionapi.ListEntriesParamsItemCondition](p.ItemCondition),
		PlatformId:    p.PlatformId,
		TagId:         p.TagId,
		Sort:          castVal[api.ListEntriesParamsSort, collectionapi.ListEntriesParamsSort](p.Sort),
		Order:         castVal[api.ListEntriesParamsOrder, collectionapi.ListEntriesParamsOrder](p.Order),
		GroupBy:       castVal[api.ListEntriesParamsGroupBy, collectionapi.ListEntriesParamsGroupBy](p.GroupBy),
		Limit:         p.Limit,
		Offset:        p.Offset,
	}
}

// relayCollectionMutation relays a mutating collection answer and, on
// success, invalidates the caller's recommendations (their library
// changed under the composition).
func (h *Handlers) relayCollectionMutation(w http.ResponseWriter, r *http.Request, sub string, res collectionclient.Result, err error) {
	if err == nil && res.Status < http.StatusMultipleChoices {
		if cerr := h.cache.InvalidateRecs(r.Context(), sub); cerr != nil {
			h.failOpenEvent(r.Context(), "recs_invalidate", cerr)
		}
	}
	h.relayCollection(w, r, res, err)
}

// ListEntries proxies the collection list matrix.
func (h *Handlers) ListEntries(w http.ResponseWriter, r *http.Request, params api.ListEntriesParams) {
	sess, _, ok := h.requireSession(w, r)
	if !ok {
		return
	}
	res, err := h.collection.ListEntries(r.Context(), sess.AccessToken, collectionListParams(params))
	h.relayCollection(w, r, res, err)
}

// CreateEntry proxies entry creation.
func (h *Handlers) CreateEntry(w http.ResponseWriter, r *http.Request) {
	sess, claims, ok := h.requireSession(w, r)
	if !ok {
		return
	}
	body, ok := readCapped(w, r)
	if !ok {
		return
	}
	res, err := h.collection.CreateEntry(r.Context(), sess.AccessToken, body)
	h.relayCollectionMutation(w, r, claims.Sub, res, err)
}

// GetEntry proxies a single entry read.
func (h *Handlers) GetEntry(w http.ResponseWriter, r *http.Request, entryId openapi_types.UUID) {
	sess, _, ok := h.requireSession(w, r)
	if !ok {
		return
	}
	res, err := h.collection.GetEntry(r.Context(), sess.AccessToken, entryId)
	h.relayCollection(w, r, res, err)
}

// UpdateEntry proxies the full-state replace.
func (h *Handlers) UpdateEntry(w http.ResponseWriter, r *http.Request, entryId openapi_types.UUID) {
	sess, claims, ok := h.requireSession(w, r)
	if !ok {
		return
	}
	body, ok := readCapped(w, r)
	if !ok {
		return
	}
	res, err := h.collection.UpdateEntry(r.Context(), sess.AccessToken, entryId, body)
	h.relayCollectionMutation(w, r, claims.Sub, res, err)
}

// DeleteEntry proxies entry deletion.
func (h *Handlers) DeleteEntry(w http.ResponseWriter, r *http.Request, entryId openapi_types.UUID) {
	sess, claims, ok := h.requireSession(w, r)
	if !ok {
		return
	}
	res, err := h.collection.DeleteEntry(r.Context(), sess.AccessToken, entryId)
	h.relayCollectionMutation(w, r, claims.Sub, res, err)
}

// AckEntryRegionMismatch proxies the region-mismatch banner dismiss.
// A stamp-only ack, not a composition change, so it relays plain
// (no recommendations invalidation) - the same choice as
// AckSubmissionResolution below.
func (h *Handlers) AckEntryRegionMismatch(w http.ResponseWriter, r *http.Request, entryId openapi_types.UUID) {
	sess, _, ok := h.requireSession(w, r)
	if !ok {
		return
	}
	res, err := h.collection.AckRegionMismatch(r.Context(), sess.AccessToken, entryId)
	h.relayCollection(w, r, res, err)
}

// ReorderEntry proxies the backlog drag.
func (h *Handlers) ReorderEntry(w http.ResponseWriter, r *http.Request, entryId openapi_types.UUID) {
	sess, claims, ok := h.requireSession(w, r)
	if !ok {
		return
	}
	body, ok := readCapped(w, r)
	if !ok {
		return
	}
	res, err := h.collection.ReorderEntry(r.Context(), sess.AccessToken, entryId, body)
	h.relayCollectionMutation(w, r, claims.Sub, res, err)
}

// BulkUpdateEntries proxies the transactional bulk tag/status/
// storage-location update (browser body untouched; collection owns
// the guards, the per-entry tag cap, and the all-or-nothing
// transaction). A mutation like every other entry write, so it
// invalidates recommendations the same way.
func (h *Handlers) BulkUpdateEntries(w http.ResponseWriter, r *http.Request) {
	sess, claims, ok := h.requireSession(w, r)
	if !ok {
		return
	}
	body, ok := readCapped(w, r)
	if !ok {
		return
	}
	res, err := h.collection.BulkUpdateEntries(r.Context(), sess.AccessToken, body)
	h.relayCollectionMutation(w, r, claims.Sub, res, err)
}

// ListTags / CreateTag / RenameTag / DeleteTag proxy the tag surface.
func (h *Handlers) ListTags(w http.ResponseWriter, r *http.Request) {
	sess, _, ok := h.requireSession(w, r)
	if !ok {
		return
	}
	res, err := h.collection.ListTags(r.Context(), sess.AccessToken)
	h.relayCollection(w, r, res, err)
}

func (h *Handlers) CreateTag(w http.ResponseWriter, r *http.Request) {
	sess, _, ok := h.requireSession(w, r)
	if !ok {
		return
	}
	body, ok := readCapped(w, r)
	if !ok {
		return
	}
	res, err := h.collection.CreateTag(r.Context(), sess.AccessToken, body)
	h.relayCollection(w, r, res, err)
}

func (h *Handlers) RenameTag(w http.ResponseWriter, r *http.Request, tagId openapi_types.UUID) {
	sess, _, ok := h.requireSession(w, r)
	if !ok {
		return
	}
	body, ok := readCapped(w, r)
	if !ok {
		return
	}
	res, err := h.collection.RenameTag(r.Context(), sess.AccessToken, tagId, body)
	h.relayCollection(w, r, res, err)
}

func (h *Handlers) DeleteTag(w http.ResponseWriter, r *http.Request, tagId openapi_types.UUID) {
	sess, _, ok := h.requireSession(w, r)
	if !ok {
		return
	}
	res, err := h.collection.DeleteTag(r.Context(), sess.AccessToken, tagId)
	h.relayCollection(w, r, res, err)
}

// publishIfListed fires the social publish event after a successful
// view write whose RESULT landed visibility=listed - the response
// body governs, not the request body: a request that asked for
// listed but the stored view came back some other way must not fire,
// and a request that did not ask for listed but the stored view came
// back listed anyway must. Fail-open: the write itself already
// succeeded in collection; a lost event costs a feed entry until the
// next listed transition, never the write.
func (h *Handlers) publishIfListed(r *http.Request, accessToken string, res collectionclient.Result) {
	if res.Status < http.StatusOK || res.Status >= http.StatusMultipleChoices {
		return
	}
	var view struct {
		Id         uuid.UUID `json:"id"`
		Visibility string    `json:"visibility"`
	}
	if err := json.Unmarshal(res.Body, &view); err != nil || view.Visibility != "listed" || view.Id == uuid.Nil {
		return
	}
	if err := h.social.RecordPublish(r.Context(), accessToken, view.Id); err != nil {
		h.failOpenEvent(r.Context(), "social_publish_event", err)
	}
}

// ListViews / CreateView / UpdateView / DeleteView proxy saved views.
func (h *Handlers) ListViews(w http.ResponseWriter, r *http.Request) {
	sess, _, ok := h.requireSession(w, r)
	if !ok {
		return
	}
	res, err := h.collection.ListViews(r.Context(), sess.AccessToken)
	h.relayCollection(w, r, res, err)
}

func (h *Handlers) CreateView(w http.ResponseWriter, r *http.Request) {
	sess, _, ok := h.requireSession(w, r)
	if !ok {
		return
	}
	body, ok := readCapped(w, r)
	if !ok {
		return
	}
	res, err := h.collection.CreateView(r.Context(), sess.AccessToken, body)
	if err == nil {
		h.publishIfListed(r, sess.AccessToken, res)
	}
	h.relayCollection(w, r, res, err)
}

func (h *Handlers) UpdateView(w http.ResponseWriter, r *http.Request, viewId openapi_types.UUID) {
	sess, _, ok := h.requireSession(w, r)
	if !ok {
		return
	}
	body, ok := readCapped(w, r)
	if !ok {
		return
	}
	res, err := h.collection.UpdateView(r.Context(), sess.AccessToken, viewId, body)
	if err == nil {
		h.publishIfListed(r, sess.AccessToken, res)
	}
	h.relayCollection(w, r, res, err)
}

func (h *Handlers) DeleteView(w http.ResponseWriter, r *http.Request, viewId openapi_types.UUID) {
	sess, _, ok := h.requireSession(w, r)
	if !ok {
		return
	}
	res, err := h.collection.DeleteView(r.Context(), sess.AccessToken, viewId)
	h.relayCollection(w, r, res, err)
}

// collectionDashboardParams re-types the mirrored dashboard filter
// params for the generated collection client (byte-identical
// contracts; only the Go package differs).
func collectionDashboardParams(p api.GetDashboardParams) *collectionapi.GetDashboardParams {
	return &collectionapi.GetDashboardParams{
		ItemType:      castSlice[api.GetDashboardParamsItemType, collectionapi.GetDashboardParamsItemType](p.ItemType),
		Status:        castSlice[api.GetDashboardParamsStatus, collectionapi.GetDashboardParamsStatus](p.Status),
		Packaging:     castSlice[api.GetDashboardParamsPackaging, collectionapi.GetDashboardParamsPackaging](p.Packaging),
		Region:        p.Region,
		ItemCondition: castSlice[api.GetDashboardParamsItemCondition, collectionapi.GetDashboardParamsItemCondition](p.ItemCondition),
		PlatformId:    p.PlatformId,
		TagId:         p.TagId,
	}
}

// GetDashboard proxies the collection-composed dashboard (cached by
// its owner, never here - one staleness authority per data type),
// forwarding the filter dimensions.
func (h *Handlers) GetDashboard(w http.ResponseWriter, r *http.Request, params api.GetDashboardParams) {
	sess, _, ok := h.requireSession(w, r)
	if !ok {
		return
	}
	res, err := h.collection.GetDashboard(r.Context(), sess.AccessToken, collectionDashboardParams(params))
	h.relayCollection(w, r, res, err)
}

// GetValueHistory proxies the collection value-over-time series
// (single-source: the collection service owns its cache and
// invalidation; the bff never caches pass-throughs).
func (h *Handlers) GetValueHistory(w http.ResponseWriter, r *http.Request) {
	sess, _, ok := h.requireSession(w, r)
	if !ok {
		return
	}
	res, err := h.collection.GetValueHistory(r.Context(), sess.AccessToken)
	h.relayCollection(w, r, res, err)
}
