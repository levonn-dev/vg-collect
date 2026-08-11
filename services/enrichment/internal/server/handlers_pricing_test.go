// Tests for batch prices, price history, and FX rates.

package server

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/levonn-dev/vgkeep/services/enrichment/internal/fx"
	"github.com/levonn-dev/vgkeep/services/enrichment/internal/gen/api"
	"github.com/levonn-dev/vgkeep/services/enrichment/internal/store"
)

func TestBatchPrices_MatrixOfKnownUnmatchedUnknown(t *testing.T) {
	s := newStack(t)
	matched := s.resolveGame(1011, 19)
	resp := s.do(http.MethodPost, "/products/resolve", s.userToken(), map[string]any{
		"type": "game", "igdb_game_id": 1018, "platform_igdb_id": 19,
	})
	unmatched := decodeBody[api.Product](t, resp)
	ghost := "99999999-9999-9999-9999-999999999999"

	resp = s.do(http.MethodPost, "/products/prices:batch", s.userToken(), map[string]any{
		"product_ids": []string{matched.Id.String(), unmatched.Id.String(), ghost},
	})
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("batch: %d", resp.StatusCode)
	}
	out := decodeBody[api.PricesBatchResponse](t, resp)
	if len(out.Prices) != 2 {
		t.Fatalf("unknown ids must be absent: %+v", out.Prices)
	}
	m := out.Prices[matched.Id.String()]
	if m.Unmatched || m.LooseCents == nil || m.AsOf == nil {
		t.Fatalf("matched prices: %+v", m)
	}
	u := out.Prices[unmatched.Id.String()]
	if !u.Unmatched || u.LooseCents != nil {
		t.Fatalf("unmatched prices: %+v", u)
	}
	if _, ok := out.Prices[ghost]; ok {
		t.Fatal("ghost id leaked into the response")
	}
}

func TestUnitBatchPrices_CapAndBadBody(t *testing.T) {
	env := newAuthEnv(t)
	tok := env.token(t, "u1", []string{"user"})
	h := newUnitHandlers(nil, nil, nil, newStubCache())

	ids := make([]string, 501)
	for i := range ids {
		ids[i] = "11111111-1111-1111-1111-111111111111"
	}
	rec := serveUnit(t, h, env, http.MethodPost, "/products/prices:batch", tok, map[string]any{"product_ids": ids})
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("cap: %d", rec.Code)
	}

	req := httptest.NewRequest(http.MethodPost, "/products/prices:batch", bytes.NewReader([]byte("{not json")))
	req.Header.Set("Authorization", "Bearer "+tok)
	rec2 := httptest.NewRecorder()
	NewRouter(h, env.validator(), slog.New(slog.DiscardHandler), func(context.Context) error { return nil }).ServeHTTP(rec2, req)
	if rec2.Code != http.StatusBadRequest {
		t.Fatalf("bad body: %d", rec2.Code)
	}
}

func TestUnitBatchPriceHistoryGroupsAndWindows(t *testing.T) {
	env := newAuthEnv(t)
	tok := env.token(t, "u1", []string{"user"})
	fixed := time.Date(2026, time.July, 5, 12, 0, 0, 0, time.UTC)
	var gotIDs []string
	var gotSince time.Time
	loose := int64(1200)
	st := &stubStore{
		snapshotsSince: func(_ context.Context, ids []string, since time.Time) (map[string][]store.Snapshot, error) {
			gotIDs, gotSince = ids, since
			return map[string][]store.Snapshot{
				ids[0]: {{ProductID: ids[0], CapturedAt: fixed.AddDate(0, 0, -3), LooseCents: &loose}},
			}, nil
		},
	}
	h := newUnitHandlers(st, nil, nil, newStubCache())
	h.now = func() time.Time { return fixed }

	idA, idB := uuid.NewString(), uuid.NewString()
	rec := serveUnit(t, h, env, http.MethodPost, "/products/price-history:batch", tok,
		map[string]any{"product_ids": []string{idA, idB}})
	if rec.Code != http.StatusOK {
		t.Fatalf("status %d: %s", rec.Code, rec.Body)
	}
	if len(gotIDs) != 2 {
		t.Fatalf("store saw %d ids, want 2", len(gotIDs))
	}
	if want := fixed.AddDate(0, 0, -90); !gotSince.Equal(want) {
		t.Fatalf("default window: since %v, want %v", gotSince, want)
	}
	var resp struct {
		Series map[string][]struct {
			CapturedAt time.Time `json:"captured_at"`
			LooseCents *int64    `json:"loose_cents"`
		} `json:"series"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatal(err)
	}
	if len(resp.Series) != 1 || len(resp.Series[idA]) != 1 || *resp.Series[idA][0].LooseCents != 1200 {
		t.Fatalf("series wrong: %s", rec.Body)
	}
}

func TestUnitBatchPriceHistoryRejectsBadInput(t *testing.T) {
	env := newAuthEnv(t)
	tok := env.token(t, "u1", []string{"user"})
	st := &stubStore{} // any store call would panic: rejection happens first
	h := newUnitHandlers(st, nil, nil, newStubCache())
	router := NewRouter(h, env.validator(), slog.New(slog.DiscardHandler), func(context.Context) error { return nil })

	// serveUnit marshals its body param through json.Marshal, which
	// cannot produce a deliberately malformed payload or a pre-built
	// literal array of quoted ids; post issues the raw body exactly
	// like the bad-body case in TestUnitBatchPrices_CapAndBadBody above.
	post := func(body string) *httptest.ResponseRecorder {
		req := httptest.NewRequest(http.MethodPost, "/products/price-history:batch", bytes.NewReader([]byte(body)))
		req.Header.Set("Authorization", "Bearer "+tok)
		rec := httptest.NewRecorder()
		router.ServeHTTP(rec, req)
		return rec
	}

	ids := make([]string, 501)
	for i := range ids {
		ids[i] = fmt.Sprintf("%q", uuid.NewString())
	}
	over := fmt.Sprintf(`{"product_ids":[%s]}`, strings.Join(ids, ","))

	cases := []struct {
		name, body string
	}{
		{"malformed", `{"product_ids":`},
		{"too many ids", over},
		{"days too small", fmt.Sprintf(`{"product_ids":[%q],"days":0}`, uuid.NewString())},
		{"days too large", fmt.Sprintf(`{"product_ids":[%q],"days":366}`, uuid.NewString())},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			rec := post(tc.body)
			if rec.Code != http.StatusBadRequest {
				t.Fatalf("status %d, want 400", rec.Code)
			}
			if !strings.Contains(rec.Body.String(), "invalid_body") {
				t.Fatalf("problem code missing: %s", rec.Body)
			}
		})
	}
}

func TestUnitBatchPriceHistoryStoreFailureIs500(t *testing.T) {
	env := newAuthEnv(t)
	tok := env.token(t, "u1", []string{"user"})
	st := &stubStore{
		snapshotsSince: func(context.Context, []string, time.Time) (map[string][]store.Snapshot, error) {
			return nil, errors.New("mongo down")
		},
	}
	h := newUnitHandlers(st, nil, nil, newStubCache())
	rec := serveUnit(t, h, env, http.MethodPost, "/products/price-history:batch", tok,
		map[string]any{"product_ids": []string{uuid.NewString()}})
	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("status %d, want 500", rec.Code)
	}
}

// ---------------------------------------------------------------
// FX rates
// ---------------------------------------------------------------

func TestUnitGetFxLatest_ServesSnapshot(t *testing.T) {
	rates := &stubFX{latest: func(context.Context) (fx.Rates, error) {
		return fx.Rates{Base: "USD", Date: "2026-07-01", Rates: map[string]float64{"EUR": 0.5, "JPY": 150}}, nil
	}}
	// Build the server through this file's usual harness, substituting
	// the fx stub; then issue an authed GET /fx/latest the same way the
	// neighboring GET tests do.
	rec := doAuthedFxRequest(t, rates)
	if rec.Code != http.StatusOK {
		t.Fatalf("status: %d body %s", rec.Code, rec.Body.String())
	}
	var got struct {
		Base  string             `json:"base"`
		Date  string             `json:"date"`
		Rates map[string]float64 `json:"rates"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if got.Base != "USD" || got.Date != "2026-07-01" || got.Rates["EUR"] != 0.5 || got.Rates["JPY"] != 150 {
		t.Fatalf("snapshot: %+v", got)
	}
}

func TestUnitGetFxLatest_ColdFailureAnswers502(t *testing.T) {
	rates := &stubFX{latest: func(context.Context) (fx.Rates, error) {
		return fx.Rates{}, errors.New("upstream down")
	}}
	rec := doAuthedFxRequest(t, rates)
	if rec.Code != http.StatusBadGateway {
		t.Fatalf("status: %d, want 502", rec.Code)
	}
	if !strings.Contains(rec.Body.String(), "upstream_unavailable") {
		t.Fatalf("problem code missing: %s", rec.Body.String())
	}
}
