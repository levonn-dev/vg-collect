package server

import (
	"errors"
	"net/http"

	"github.com/levonn-dev/vgkeep/libs/go/contract/common"
	"github.com/levonn-dev/vgkeep/services/user/internal/gen/api"
	"github.com/levonn-dev/vgkeep/services/user/internal/store"
)

// /shared handlers serve any authenticated caller: no sub-scoping,
// ProfileCard projection only (never email or roles). Unknown and private
// answer the same 404 (anti-oracle). ids (maxItems: 100) and q
// (maxLength: 64) are enforced by specval, not checked here.

const searchLimit = 20

func toCard(u store.User) api.ProfileCard {
	return api.ProfileCard{
		UserId:            u.ID,
		Handle:            u.Handle,
		AvatarUrl:         u.AvatarURL,
		ProfileVisibility: common.Visibility(u.ProfileVisibility),
	}
}

func (h *Handlers) GetSharedProfile(w http.ResponseWriter, r *http.Request, handle string) {
	u, err := h.store.GetByHandle(r.Context(), store.NormalizeHandle(handle))
	if errors.Is(err, store.ErrNotFound) || (err == nil && u.ProfileVisibility == "private") {
		problem(w, r, http.StatusNotFound, "profile_not_found", "no such profile")
		return
	}
	if err != nil {
		h.internalError(w, r, "shared_profile", "profile lookup failed", err)
		return
	}
	writeJSON(w, http.StatusOK, toCard(u))
}

func (h *Handlers) GetSharedProfilesByIds(w http.ResponseWriter, r *http.Request, params api.GetSharedProfilesByIdsParams) {
	users, err := h.store.GetByIDs(r.Context(), params.Ids)
	if err != nil {
		h.internalError(w, r, "shared_by_ids", "profile batch failed", err)
		return
	}
	cards := make([]api.ProfileCard, len(users))
	for i, u := range users {
		cards[i] = toCard(u)
	}
	writeJSON(w, http.StatusOK, map[string][]api.ProfileCard{"profiles": cards})
}

func (h *Handlers) SearchSharedProfiles(w http.ResponseWriter, r *http.Request, params api.SearchSharedProfilesParams) {
	folded := store.NormalizeHandle(params.Q)
	if folded == "" {
		writeJSON(w, http.StatusOK, map[string][]api.ProfileCard{"profiles": {}})
		return
	}
	users, err := h.store.SearchListed(r.Context(), folded, searchLimit)
	if err != nil {
		h.internalError(w, r, "shared_search", "search failed", err)
		return
	}
	cards := make([]api.ProfileCard, len(users))
	for i, u := range users {
		cards[i] = toCard(u)
	}
	writeJSON(w, http.StatusOK, map[string][]api.ProfileCard{"profiles": cards})
}
