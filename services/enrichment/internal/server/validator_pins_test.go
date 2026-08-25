// Validator-path pins: each case drives a request through the full
// handler stack and asserts the status + code specval's
// request-validation middleware answers with.
//
// ClampReversal (in TestValidatorPath_ListCommunityProducts_LimitOverMax_ClampReversal)
// names a real behavior change: the community list's limit used to
// silently clamp out-of-range values instead of rejecting them; specval now 400s invalid_param.
package server

import (
	"context"
	"net/http"
	"testing"

	"github.com/google/uuid"

	"github.com/levonn-dev/vgkeep/libs/go/reqtest"
	"github.com/levonn-dev/vgkeep/services/enrichment/internal/store"
)

// TestValidatorPath_CreateCommunityProduct_BadTypeEnum pins the
// CommunityProductSpec.type enum contract.
func TestValidatorPath_CreateCommunityProduct_BadTypeEnum(t *testing.T) {
	env := newAuthEnv(t)
	admin := env.token(t, uuid.NewString(), []string{"user", "admin"})
	h := newUnitHandlers(&stubStore{}, &stubGames{}, &stubPrices{}, newStubCache())
	rec := serveUnit(t, h, env, http.MethodPost, "/admin/products", admin,
		map[string]any{"type": "pc_listing", "name": "X"})
	reqtest.AssertProblemRec(t, rec, http.StatusBadRequest, "invalid_body")
}

// TestValidatorPath_BatchPriceHistory_DaysOverMax pins the
// PriceHistoryRequest.days maximum(365) contract cap.
func TestValidatorPath_BatchPriceHistory_DaysOverMax(t *testing.T) {
	env := newAuthEnv(t)
	tok := env.token(t, "u1", []string{"user"})
	h := newUnitHandlers(&stubStore{}, &stubGames{}, &stubPrices{}, newStubCache())
	rec := serveUnit(t, h, env, http.MethodPost, "/products/price-history:batch", tok,
		map[string]any{"product_ids": []string{uuid.NewString()}, "days": 400})
	reqtest.AssertProblemRec(t, rec, http.StatusBadRequest, "invalid_body")
}

// TestValidatorPath_ListCommunityProducts_LimitOverMax_ClampReversal
// is the deliberate clamp reversal: see the file comment.
func TestValidatorPath_ListCommunityProducts_LimitOverMax_ClampReversal(t *testing.T) {
	env := newAuthEnv(t)
	admin := env.token(t, uuid.NewString(), []string{"user", "admin"})
	st := &stubStore{listCommunityProductsPage: func(context.Context, int, int) ([]store.Product, int64, error) {
		return nil, 0, nil
	}}
	h := newUnitHandlers(st, &stubGames{}, &stubPrices{}, newStubCache())
	rec := serveUnit(t, h, env, http.MethodGet, "/admin/products/community?limit=9999", admin, nil)
	reqtest.AssertProblemRec(t, rec, http.StatusBadRequest, "invalid_param")
}
