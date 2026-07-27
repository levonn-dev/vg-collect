package server

import (
	"errors"
	"net/http"

	"github.com/levonn-dev/vg-collect/services/user/internal/gen/api"
	"github.com/levonn-dev/vg-collect/services/user/internal/store"
)

// The /shared handlers serve any authenticated caller: no sub-scoping,
// visibility-filtered, ProfileCard projection only (never email or
// roles). Unknown and private answer the same 404 so resolution is
// not an existence oracle.

const searchLimit = 20

// maxIDsBatch and maxQueryLength enforce the size limits api/user.yaml
// declares (maxItems: 100 on GetSharedProfilesByIds' ids, maxLength: 64
// on SearchSharedProfiles' q). This service has no schema-validation
// middleware -- only the generated param binder runs, and it does not
// check these bounds -- so the handlers enforce them directly.
const (
	maxIDsBatch    = 100
	maxQueryLength = 64
)

func toCard(u store.User) api.ProfileCard {
	return api.ProfileCard{
		UserId:            u.ID,
		Handle:            u.Handle,
		AvatarUrl:         u.AvatarURL,
		ProfileVisibility: api.ProfileCardProfileVisibility(u.ProfileVisibility),
	}
}

func (h *Handlers) GetSharedProfile(w http.ResponseWriter, r *http.Request, handle string) {
	u, err := h.store.GetByHandle(r.Context(), store.NormalizeHandle(handle))
	if errors.Is(err, store.ErrNotFound) || (err == nil && u.ProfileVisibility == "private") {
		problem(w, r, http.StatusNotFound, "profile_not_found", "no such profile")
		return
	}
	if err != nil {
		problem(w, r, http.StatusInternalServerError, "internal", "profile lookup failed")
		return
	}
	writeJSON(w, http.StatusOK, toCard(u))
}

func (h *Handlers) GetSharedProfilesByIds(w http.ResponseWriter, r *http.Request, params api.GetSharedProfilesByIdsParams) {
	if len(params.Ids) > maxIDsBatch {
		problem(w, r, http.StatusBadRequest, "too_many_ids", "ids must contain at most 100 entries")
		return
	}
	users, err := h.store.GetByIDs(r.Context(), params.Ids)
	if err != nil {
		problem(w, r, http.StatusInternalServerError, "internal", "profile batch failed")
		return
	}
	cards := make([]api.ProfileCard, len(users))
	for i, u := range users {
		cards[i] = toCard(u)
	}
	writeJSON(w, http.StatusOK, map[string][]api.ProfileCard{"profiles": cards})
}

func (h *Handlers) SearchSharedProfiles(w http.ResponseWriter, r *http.Request, params api.SearchSharedProfilesParams) {
	if len(params.Q) > maxQueryLength {
		problem(w, r, http.StatusBadRequest, "query_too_long", "q must be at most 64 characters")
		return
	}
	folded := store.NormalizeHandle(params.Q)
	if folded == "" {
		writeJSON(w, http.StatusOK, map[string][]api.ProfileCard{"profiles": {}})
		return
	}
	users, err := h.store.SearchListed(r.Context(), folded, searchLimit)
	if err != nil {
		problem(w, r, http.StatusInternalServerError, "internal", "search failed")
		return
	}
	cards := make([]api.ProfileCard, len(users))
	for i, u := range users {
		cards[i] = toCard(u)
	}
	writeJSON(w, http.StatusOK, map[string][]api.ProfileCard{"profiles": cards})
}
