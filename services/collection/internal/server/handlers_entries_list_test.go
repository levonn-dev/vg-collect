// Tests for the entry list pipeline: query-param validation,
// value sorting, grouping, and pagination.

package server_test

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"net/http"
	"testing"

	"github.com/google/uuid"

	"github.com/levonn-dev/vgkeep/libs/go/contract/enrichapi"
	"github.com/levonn-dev/vgkeep/services/collection/internal/enrichmentclient"
	"github.com/levonn-dev/vgkeep/services/collection/internal/store"
)

// listedEntry builds a stored entry with knobs the list tests need.
func listedEntry(user uuid.UUID, name string, mut func(*store.Entry)) store.Entry {
	e := storedGameEntry(user)
	e.ID = uuid.New()
	e.ProductID = new(uuid.New())
	e.DisplayName = name
	if mut != nil {
		mut(&e)
	}
	return e
}

// pagedStub serves a fixed entry set through the SQL-paginated list path
// (CountEntriesFiltered + ListEntriesPage), mimicking LIMIT/OFFSET so the
// count and the returned page stay consistent.
func pagedStub(all []store.Entry) *stubStore {
	return &stubStore{
		countEntriesFiltered: func(context.Context, uuid.UUID, store.Filters) (int, error) {
			return len(all), nil
		},
		listEntriesPage: func(_ context.Context, _ uuid.UUID, _ store.Filters, limit, offset int) ([]store.Entry, error) {
			if offset >= len(all) {
				return nil, nil
			}
			end := offset + limit
			if end > len(all) {
				end = len(all)
			}
			return all[offset:end], nil
		},
	}
}

func TestUnitListEntries_ValueSortAndComposition(t *testing.T) {
	user := uuid.New()
	cheap := listedEntry(user, "Cheap", nil)
	dear := listedEntry(user, "Dear", nil)
	noprice := listedEntry(user, "Disabled", func(e *store.Entry) { e.PricingMode = "disabled" })
	st := &stubStore{listEntries: func(_ context.Context, _ uuid.UUID, f store.Filters) ([]store.Entry, error) {
		if f.Sort != "value" || f.Order != "desc" {
			t.Fatalf("filters must pass through: %+v", f)
		}
		return []store.Entry{cheap, dear, noprice}, nil
	}}
	enrich := &stubEnrichment{batchPrices: func(_ context.Context, _ string, ids []uuid.UUID) (map[string]enrichapi.ProductPrices, error) {
		// The disabled entry contributes no id.
		if len(ids) != 2 {
			t.Fatalf("effective ids: %v", ids)
		}
		lo1, lo2 := int64(1000), int64(9000)
		return map[string]enrichapi.ProductPrices{
			cheap.ProductID.String(): {CibCents: &lo1},
			dear.ProductID.String():  {CibCents: &lo2},
		}, nil
	}}
	srv, a := newUnitServer(t, st, enrich, newStubCache())
	resp := do(t, http.MethodGet, srv.URL+"/entries?sort=value&order=desc", a.token(t, user.String()), nil)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status %d", resp.StatusCode)
	}
	var got struct {
		PricingAvailable bool `json:"pricing_available"`
		Entries          []struct {
			DisplayName string `json:"display_name"`
			ValueCents  *int64 `json:"value_cents"`
		} `json:"entries"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&got); err != nil {
		t.Fatal(err)
	}
	if !got.PricingAvailable || len(got.Entries) != 3 {
		t.Fatalf("list: %+v", got)
	}
	// Value desc, null (disabled) last.
	if got.Entries[0].DisplayName != "Dear" || got.Entries[1].DisplayName != "Cheap" || got.Entries[2].DisplayName != "Disabled" {
		t.Fatalf("order: %+v", got.Entries)
	}
	if got.Entries[2].ValueCents != nil {
		t.Fatal("disabled entry must have a null value")
	}
}

// TestUnitListEntries_ValueSortPagesAfterComposing guards against slicing the
// page before pricing/re-sorting: store order here is the exact reverse of
// value order, with the priciest row two spots past a limit=2 page.
func TestUnitListEntries_ValueSortPagesAfterComposing(t *testing.T) {
	user := uuid.New()
	p1000 := listedEntry(user, "P1000", nil)
	p2000 := listedEntry(user, "P2000", nil)
	p3000 := listedEntry(user, "P3000", nil)
	p4000 := listedEntry(user, "P4000", nil)
	storeOrder := []store.Entry{p1000, p2000, p3000, p4000}
	prices := map[uuid.UUID]int64{
		*p1000.ProductID: 1000,
		*p2000.ProductID: 2000,
		*p3000.ProductID: 3000,
		*p4000.ProductID: 4000,
	}
	st := &stubStore{listEntries: func(_ context.Context, _ uuid.UUID, f store.Filters) ([]store.Entry, error) {
		if f.Sort != "value" || f.Order != "desc" {
			t.Fatalf("filters must pass through: %+v", f)
		}
		return storeOrder, nil
	}}
	var calls int
	enrich := &stubEnrichment{batchPrices: func(_ context.Context, _ string, ids []uuid.UUID) (map[string]enrichapi.ProductPrices, error) {
		calls++
		out := map[string]enrichapi.ProductPrices{}
		for _, id := range ids {
			v := prices[id]
			out[id.String()] = enrichapi.ProductPrices{CibCents: &v}
		}
		return out, nil
	}}
	srv, a := newUnitServer(t, st, enrich, newStubCache())

	var got struct {
		TotalCount int `json:"total_count"`
		Entries    []struct {
			DisplayName string `json:"display_name"`
			ValueCents  *int64 `json:"value_cents"`
		} `json:"entries"`
	}
	resp := do(t, http.MethodGet, srv.URL+"/entries?sort=value&order=desc&limit=2&offset=0", a.token(t, user.String()), nil)
	if err := json.NewDecoder(resp.Body).Decode(&got); err != nil {
		t.Fatal(err)
	}
	if got.TotalCount != 4 || len(got.Entries) != 2 ||
		got.Entries[0].DisplayName != "P4000" || got.Entries[1].DisplayName != "P3000" {
		t.Fatalf("top-2 by value (offset 0): %+v", got.Entries)
	}
	if calls != 1 {
		t.Fatalf("one batch call per request: %d", calls)
	}

	got.Entries = nil
	resp = do(t, http.MethodGet, srv.URL+"/entries?sort=value&order=desc&limit=2&offset=2", a.token(t, user.String()), nil)
	if err := json.NewDecoder(resp.Body).Decode(&got); err != nil {
		t.Fatal(err)
	}
	if got.TotalCount != 4 || len(got.Entries) != 2 ||
		got.Entries[0].DisplayName != "P2000" || got.Entries[1].DisplayName != "P1000" {
		t.Fatalf("next-2 by value (offset 2): %+v", got.Entries)
	}
	if calls != 2 {
		t.Fatalf("one batch call per request: %d", calls)
	}
}

func TestUnitListEntries_DegradedPricing(t *testing.T) {
	user := uuid.New()
	e := listedEntry(user, "Solo", nil)
	st := pagedStub([]store.Entry{e})
	enrich := &stubEnrichment{batchPrices: func(context.Context, string, []uuid.UUID) (map[string]enrichapi.ProductPrices, error) {
		return nil, enrichmentclient.ErrUnavailable
	}}
	srv, a := newUnitServer(t, st, enrich, newStubCache())
	resp := do(t, http.MethodGet, srv.URL+"/entries", a.token(t, user.String()), nil)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("degradation must still answer 200, got %d", resp.StatusCode)
	}
	var got struct {
		PricingAvailable bool `json:"pricing_available"`
		Entries          []struct {
			ValueCents *int64 `json:"value_cents"`
		} `json:"entries"`
	}
	_ = json.NewDecoder(resp.Body).Decode(&got)
	if got.PricingAvailable || got.Entries[0].ValueCents != nil {
		t.Fatalf("degraded shape: %+v", got)
	}
}

func TestUnitListEntries_Grouping(t *testing.T) {
	user := uuid.New()
	rpg := store.TagRef{ID: uuid.New(), Name: "rpg"}
	fav := store.TagRef{ID: uuid.New(), Name: "fav"}
	multi := listedEntry(user, "Multi", func(e *store.Entry) { e.Tags = []store.TagRef{rpg, fav}; e.PricingMode = "disabled" })
	bare := listedEntry(user, "Bare", func(e *store.Entry) { e.PricingMode = "disabled" })
	st := pagedStub([]store.Entry{multi, bare})
	srv, a := newUnitServer(t, st, &stubEnrichment{}, newStubCache())
	resp := do(t, http.MethodGet, srv.URL+"/entries?group_by=tag", a.token(t, user.String()), nil)
	var got struct {
		Entries *[]any `json:"entries"`
		Groups  []struct {
			Label   string `json:"label"`
			Entries []struct {
				DisplayName string `json:"display_name"`
			} `json:"entries"`
		} `json:"groups"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&got); err != nil {
		t.Fatal(err)
	}
	if got.Entries != nil {
		t.Fatal("grouped responses must not carry the flat list")
	}
	// fav, rpg (label asc), Untagged last; Multi appears in both tag groups.
	if len(got.Groups) != 3 || got.Groups[0].Label != "fav" || got.Groups[1].Label != "rpg" || got.Groups[2].Label != "Untagged" {
		t.Fatalf("groups: %+v", got.Groups)
	}
	if got.Groups[0].Entries[0].DisplayName != "Multi" || got.Groups[1].Entries[0].DisplayName != "Multi" ||
		got.Groups[2].Entries[0].DisplayName != "Bare" {
		t.Fatalf("membership: %+v", got.Groups)
	}
}

// TestUnitListEntries_GroupingDimensions covers group_by dimensions beyond
// tag: each has its own catch-all label for missing fields; item_type (always populated) has none.
func TestUnitListEntries_GroupingDimensions(t *testing.T) {
	user := uuid.New()

	t.Run("platform: named groups ascending, Unknown last", func(t *testing.T) {
		snes := listedEntry(user, "SNES Game", func(e *store.Entry) {
			e.PricingMode = "disabled"
			e.PlatformName = new("SNES")
		})
		genesis := listedEntry(user, "Genesis Game", func(e *store.Entry) {
			e.PricingMode = "disabled"
			e.PlatformName = new("Genesis")
		})
		unknown := listedEntry(user, "No Platform Game", func(e *store.Entry) {
			e.PricingMode = "disabled"
			e.PlatformName = nil
		})
		st := pagedStub([]store.Entry{snes, genesis, unknown})
		srv, a := newUnitServer(t, st, &stubEnrichment{}, newStubCache())
		resp := do(t, http.MethodGet, srv.URL+"/entries?group_by=platform", a.token(t, user.String()), nil)
		var got struct {
			Groups []struct {
				Label   string `json:"label"`
				Entries []struct {
					DisplayName string `json:"display_name"`
				} `json:"entries"`
			} `json:"groups"`
		}
		if err := json.NewDecoder(resp.Body).Decode(&got); err != nil {
			t.Fatal(err)
		}
		if len(got.Groups) != 3 || got.Groups[0].Label != "Genesis" || got.Groups[1].Label != "SNES" || got.Groups[2].Label != "Unknown" {
			t.Fatalf("platform groups: %+v", got.Groups)
		}
		if got.Groups[0].Entries[0].DisplayName != "Genesis Game" ||
			got.Groups[1].Entries[0].DisplayName != "SNES Game" ||
			got.Groups[2].Entries[0].DisplayName != "No Platform Game" {
			t.Fatalf("platform membership: %+v", got.Groups)
		}
	})

	t.Run("location: named groups ascending, Unassigned last", func(t *testing.T) {
		closet := listedEntry(user, "Closet Game", func(e *store.Entry) {
			e.PricingMode = "disabled"
			e.StorageLocation = new("Closet")
		})
		shelf := listedEntry(user, "Shelf Game", func(e *store.Entry) {
			e.PricingMode = "disabled"
			e.StorageLocation = new("Shelf A")
		})
		unassigned := listedEntry(user, "No Location Game", func(e *store.Entry) {
			e.PricingMode = "disabled"
			e.StorageLocation = nil
		})
		st := pagedStub([]store.Entry{closet, shelf, unassigned})
		srv, a := newUnitServer(t, st, &stubEnrichment{}, newStubCache())
		resp := do(t, http.MethodGet, srv.URL+"/entries?group_by=location", a.token(t, user.String()), nil)
		var got struct {
			Groups []struct {
				Label   string `json:"label"`
				Entries []struct {
					DisplayName string `json:"display_name"`
				} `json:"entries"`
			} `json:"groups"`
		}
		if err := json.NewDecoder(resp.Body).Decode(&got); err != nil {
			t.Fatal(err)
		}
		if len(got.Groups) != 3 || got.Groups[0].Label != "Closet" || got.Groups[1].Label != "Shelf A" || got.Groups[2].Label != "Unassigned" {
			t.Fatalf("location groups: %+v", got.Groups)
		}
		if got.Groups[0].Entries[0].DisplayName != "Closet Game" ||
			got.Groups[1].Entries[0].DisplayName != "Shelf Game" ||
			got.Groups[2].Entries[0].DisplayName != "No Location Game" {
			t.Fatalf("location membership: %+v", got.Groups)
		}
	})

	t.Run("item_type: basic partition, no catch-all", func(t *testing.T) {
		game := listedEntry(user, "Game Entry", func(e *store.Entry) {
			e.PricingMode = "disabled"
			e.ItemType = "game"
		})
		console := listedEntry(user, "Console Entry", func(e *store.Entry) {
			e.PricingMode = "disabled"
			e.ItemType = "console"
		})
		accessory := listedEntry(user, "Accessory Entry", func(e *store.Entry) {
			e.PricingMode = "disabled"
			e.ItemType = "accessory"
		})
		st := pagedStub([]store.Entry{game, console, accessory})
		srv, a := newUnitServer(t, st, &stubEnrichment{}, newStubCache())
		resp := do(t, http.MethodGet, srv.URL+"/entries?group_by=item_type", a.token(t, user.String()), nil)
		var got struct {
			Groups []struct {
				Label   string `json:"label"`
				Entries []struct {
					DisplayName string `json:"display_name"`
				} `json:"entries"`
			} `json:"groups"`
		}
		if err := json.NewDecoder(resp.Body).Decode(&got); err != nil {
			t.Fatal(err)
		}
		if len(got.Groups) != 3 || got.Groups[0].Label != "accessory" || got.Groups[1].Label != "console" || got.Groups[2].Label != "game" {
			t.Fatalf("item_type groups: %+v", got.Groups)
		}
		if got.Groups[0].Entries[0].DisplayName != "Accessory Entry" ||
			got.Groups[1].Entries[0].DisplayName != "Console Entry" ||
			got.Groups[2].Entries[0].DisplayName != "Game Entry" {
			t.Fatalf("item_type membership: %+v", got.Groups)
		}
	})
}

func TestUnitListEntries_Pagination(t *testing.T) {
	user := uuid.New()
	proxyTarget := uuid.New()
	game0 := listedEntry(user, "Game 0", nil)
	game1 := listedEntry(user, "Game 1", nil)
	game2 := listedEntry(user, "Game 2", func(e *store.Entry) {
		e.PricingMode = "proxy"
		e.PricingProductID = &proxyTarget
	})
	game3 := listedEntry(user, "Game 3", nil) // auto: prices off its own product_id
	game4 := listedEntry(user, "Game 4", func(e *store.Entry) { e.PricingMode = "disabled" })
	all := []store.Entry{game0, game1, game2, game3, game4}
	st := pagedStub(all)
	var calls int
	var capturedIDs []uuid.UUID
	enrich := &stubEnrichment{batchPrices: func(_ context.Context, _ string, ids []uuid.UUID) (map[string]enrichapi.ProductPrices, error) {
		calls++
		capturedIDs = append(capturedIDs, ids...)
		return map[string]enrichapi.ProductPrices{}, nil
	}}
	srv, a := newUnitServer(t, st, enrich, newStubCache())

	// The page (offset 2, limit 3) spans one entry of each pricing mode: proxy, auto, disabled.
	resp := do(t, http.MethodGet, srv.URL+"/entries?limit=3&offset=2", a.token(t, user.String()), nil)
	var got struct {
		TotalCount int `json:"total_count"`
		Entries    []struct {
			DisplayName string `json:"display_name"`
			ValueCents  *int64 `json:"value_cents"`
		} `json:"entries"`
	}
	_ = json.NewDecoder(resp.Body).Decode(&got)
	if got.TotalCount != 5 || len(got.Entries) != 3 ||
		got.Entries[0].DisplayName != "Game 2" || got.Entries[1].DisplayName != "Game 3" ||
		got.Entries[2].DisplayName != "Game 4" {
		t.Fatalf("page: %+v", got)
	}
	if got.Entries[2].ValueCents != nil {
		t.Fatal("the disabled entry must carry no value")
	}
	// Exactly one batched call for the request...
	if calls != 1 {
		t.Fatalf("one batch call per request: %d", calls)
	}
	// ...over the PAGE's effective ids only: proxy resolves through its
	// override, auto through its own product, disabled contributes none.
	if len(capturedIDs) != 2 || capturedIDs[0] != proxyTarget || capturedIDs[1] != *game3.ProductID {
		t.Fatalf("page-only effective ids: %v", capturedIDs)
	}

	// An offset past the end is an empty page, not an error.
	resp = do(t, http.MethodGet, srv.URL+"/entries?offset=99", a.token(t, user.String()), nil)
	got.Entries = nil
	_ = json.NewDecoder(resp.Body).Decode(&got)
	if got.TotalCount != 5 || len(got.Entries) != 0 {
		t.Fatalf("past-the-end page: %+v", got)
	}

	// Bounds validate.
	resp = do(t, http.MethodGet, srv.URL+"/entries?limit=501", a.token(t, user.String()), nil)
	wantProblem(t, resp, http.StatusBadRequest, "invalid_param")

	// Neither the empty page nor the validation failure placed a spurious batch call.
	if calls != 1 {
		t.Fatalf("no spurious batch calls: %d", calls)
	}
}

// TestUnitListEntries_OffsetOverflowClampsToEmptyPage pins that an
// absurd-but-legal offset answers a normal empty page, not overflowing the paging math.
func TestUnitListEntries_OffsetOverflowClampsToEmptyPage(t *testing.T) {
	user := uuid.New()
	only := listedEntry(user, "Only", nil)
	st := pagedStub([]store.Entry{only})
	srv, a := newUnitServer(t, st, &stubEnrichment{}, newStubCache())

	resp := do(t, http.MethodGet, fmt.Sprintf("%s/entries?offset=%d", srv.URL, math.MaxInt),
		a.token(t, user.String()), nil)
	body, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("an absurd but contract-legal offset must not 500: %d: %s", resp.StatusCode, body)
	}
	var got struct {
		TotalCount int   `json:"total_count"`
		Entries    []any `json:"entries"`
	}
	if err := json.Unmarshal(body, &got); err != nil {
		t.Fatal(err)
	}
	if got.TotalCount != 1 || len(got.Entries) != 0 {
		t.Fatalf("want total_count=1, empty page; got %+v", got)
	}
}

// TestUnitListEntries_DefaultSortAndOrder pins the unset-params default:
// created_at desc, and a default limit generous enough for a small set to reach the response whole.
func TestUnitListEntries_DefaultSortAndOrder(t *testing.T) {
	user := uuid.New()
	ea := listedEntry(user, "A", func(e *store.Entry) { e.PricingMode = "disabled" })
	eb := listedEntry(user, "B", func(e *store.Entry) { e.PricingMode = "disabled" })
	ec := listedEntry(user, "C", func(e *store.Entry) { e.PricingMode = "disabled" })
	assertDefault := func(f store.Filters) {
		if f.Sort != "created_at" || f.Order != "desc" {
			t.Fatalf("unset sort/order must default: %+v", f)
		}
	}
	st := &stubStore{
		countEntriesFiltered: func(_ context.Context, _ uuid.UUID, f store.Filters) (int, error) {
			assertDefault(f)
			return 3, nil
		},
		listEntriesPage: func(_ context.Context, _ uuid.UUID, f store.Filters, _, _ int) ([]store.Entry, error) {
			assertDefault(f)
			return []store.Entry{ea, eb, ec}, nil
		},
	}
	// batchPrices deliberately nil: every fixture is pricing-disabled, so a call would panic the stub.
	srv, a := newUnitServer(t, st, &stubEnrichment{}, newStubCache())
	resp := do(t, http.MethodGet, srv.URL+"/entries", a.token(t, user.String()), nil)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status %d", resp.StatusCode)
	}
	var got struct {
		TotalCount int `json:"total_count"`
		Entries    []struct {
			DisplayName string `json:"display_name"`
		} `json:"entries"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&got); err != nil {
		t.Fatal(err)
	}
	// The default limit (200) must not truncate a 3-row set, and the default offset (0) must not skip any.
	if got.TotalCount != 3 || len(got.Entries) != 3 {
		t.Fatalf("default page: %+v", got)
	}
}

// region is deliberately absent from this list: it is open-world now, so no string is a bad enum for it.
// TestUnitListEntries_StoreErrorsMapTo500 covers the three read failures the
// two list paths can hit: the value path's full fetch, and the SQL-orderable
// path's count and page reads.
func TestUnitListEntries_StoreErrorsMapTo500(t *testing.T) {
	user := uuid.New()

	t.Run("value list failure", func(t *testing.T) {
		st := &stubStore{listEntries: func(context.Context, uuid.UUID, store.Filters) ([]store.Entry, error) {
			return nil, errors.New("boom")
		}}
		srv, a := newUnitServer(t, st, &stubEnrichment{}, newStubCache())
		resp := do(t, http.MethodGet, srv.URL+"/entries?sort=value", a.token(t, user.String()), nil)
		wantProblem(t, resp, http.StatusInternalServerError, "internal")
	})

	t.Run("count failure", func(t *testing.T) {
		st := &stubStore{countEntriesFiltered: func(context.Context, uuid.UUID, store.Filters) (int, error) {
			return 0, errors.New("boom")
		}}
		srv, a := newUnitServer(t, st, &stubEnrichment{}, newStubCache())
		resp := do(t, http.MethodGet, srv.URL+"/entries", a.token(t, user.String()), nil)
		wantProblem(t, resp, http.StatusInternalServerError, "internal")
	})

	t.Run("page failure", func(t *testing.T) {
		st := &stubStore{
			countEntriesFiltered: func(context.Context, uuid.UUID, store.Filters) (int, error) { return 3, nil },
			listEntriesPage: func(context.Context, uuid.UUID, store.Filters, int, int) ([]store.Entry, error) {
				return nil, errors.New("boom")
			},
		}
		srv, a := newUnitServer(t, st, &stubEnrichment{}, newStubCache())
		resp := do(t, http.MethodGet, srv.URL+"/entries", a.token(t, user.String()), nil)
		wantProblem(t, resp, http.StatusInternalServerError, "internal")
	})
}

func TestUnitListEntries_BadEnumParam(t *testing.T) {
	srv, a := newUnitServer(t, &stubStore{}, &stubEnrichment{}, newStubCache())
	for _, q := range []string{"status=queued", "sort=alphabetical", "group_by=color", "order=up"} {
		resp := do(t, http.MethodGet, srv.URL+"/entries?"+q, a.token(t, uuid.NewString()), nil)
		wantProblem(t, resp, http.StatusBadRequest, "invalid_param")
	}
}

func TestUnitListEntries_Empty(t *testing.T) {
	st := pagedStub([]store.Entry{})
	srv, a := newUnitServer(t, st, &stubEnrichment{}, newStubCache())
	resp := do(t, http.MethodGet, srv.URL+"/entries", a.token(t, uuid.NewString()), nil)
	var got struct {
		PricingAvailable bool  `json:"pricing_available"`
		Entries          []any `json:"entries"`
	}
	_ = json.NewDecoder(resp.Body).Decode(&got)
	if !got.PricingAvailable || got.Entries == nil || len(got.Entries) != 0 {
		t.Fatalf("empty list: %+v (entries must be [], not null)", got)
	}
}

func TestListThroughTheStack(t *testing.T) {
	s := newStack(t)
	cheapID := s.enrich.addGame("Alundra", 1000, 2000, 3000)
	dearID := s.enrich.addGame("Chrono Trigger", 5000, 9000, 20000)
	sub := uuid.New()
	tok := s.auth.token(t, sub.String())

	for _, p := range []uuid.UUID{cheapID, dearID} {
		resp := do(t, http.MethodPost, s.baseURL+"/entries", tok, createBody(p, nil))
		if resp.StatusCode != http.StatusCreated {
			t.Fatalf("create: %d", resp.StatusCode)
		}
	}

	resp := do(t, http.MethodGet, s.baseURL+"/entries?sort=value&order=desc", tok, nil)
	var got struct {
		PricingAvailable bool `json:"pricing_available"`
		Entries          []struct {
			DisplayName string `json:"display_name"`
			ValueCents  *int64 `json:"value_cents"`
		} `json:"entries"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&got); err != nil {
		t.Fatal(err)
	}
	if !got.PricingAvailable || len(got.Entries) != 2 ||
		got.Entries[0].DisplayName != "Chrono Trigger" || *got.Entries[0].ValueCents != 9000 ||
		got.Entries[1].DisplayName != "Alundra" || *got.Entries[1].ValueCents != 2000 {
		t.Fatalf("value sort through the stack: %+v", got)
	}

	// group_by=status yields one backlog group holding both.
	resp = do(t, http.MethodGet, s.baseURL+"/entries?group_by=status", tok, nil)
	var grouped struct {
		Groups []struct {
			Label   string `json:"label"`
			Entries []any  `json:"entries"`
		} `json:"groups"`
	}
	_ = json.NewDecoder(resp.Body).Decode(&grouped)
	if len(grouped.Groups) != 1 || grouped.Groups[0].Label != "backlog" || len(grouped.Groups[0].Entries) != 2 {
		t.Fatalf("groups: %+v", grouped.Groups)
	}

	// Enrichment down: the list still answers, flagged.
	s.enrich.mu.Lock()
	s.enrich.down = true
	s.enrich.mu.Unlock()
	resp = do(t, http.MethodGet, s.baseURL+"/entries", tok, nil)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("degraded list must answer 200, got %d", resp.StatusCode)
	}
	var degraded struct {
		PricingAvailable bool `json:"pricing_available"`
	}
	_ = json.NewDecoder(resp.Body).Decode(&degraded)
	if degraded.PricingAvailable {
		t.Fatal("degradation must be flagged")
	}
}

// TestUnitListEntries_MixedCustomAndProxyComposition pins that a custom-priced
// entry composes alongside an enrichment-priced one, sorting correctly against each other.
func TestUnitListEntries_MixedCustomAndProxyComposition(t *testing.T) {
	user := uuid.New()
	proxyTarget := uuid.New()
	proxied := listedEntry(user, "Proxied", func(e *store.Entry) {
		e.PricingMode = "proxy"
		e.PricingProductID = &proxyTarget
	})
	custom := listedEntry(user, "Custom", func(e *store.Entry) {
		e.PricingMode = "custom"
		e.CustomValueCents = new(int64(5000))
	})
	st := &stubStore{listEntries: func(_ context.Context, _ uuid.UUID, f store.Filters) ([]store.Entry, error) {
		if f.Sort != "value" || f.Order != "asc" {
			t.Fatalf("filters must pass through: %+v", f)
		}
		return []store.Entry{proxied, custom}, nil
	}}
	enrich := &stubEnrichment{batchPrices: pricedAs(1000, 2000, 3000)}
	srv, a := newUnitServer(t, st, enrich, newStubCache())

	resp := do(t, http.MethodGet, srv.URL+"/entries?sort=value&order=asc", a.token(t, user.String()), nil)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status %d", resp.StatusCode)
	}
	var got struct {
		Entries []struct {
			DisplayName string `json:"display_name"`
			ValueCents  *int64 `json:"value_cents"`
		} `json:"entries"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&got); err != nil {
		t.Fatal(err)
	}
	if len(got.Entries) != 2 {
		t.Fatalf("entries: %+v", got.Entries)
	}
	if got.Entries[0].DisplayName != "Proxied" || got.Entries[1].DisplayName != "Custom" {
		t.Fatalf("order: %+v", got.Entries)
	}
	if got.Entries[0].ValueCents == nil || *got.Entries[0].ValueCents != 2000 {
		t.Fatalf("proxy value: %v", got.Entries[0].ValueCents)
	}
	if got.Entries[1].ValueCents == nil || *got.Entries[1].ValueCents != 5000 {
		t.Fatalf("custom value: %v", got.Entries[1].ValueCents)
	}
}
