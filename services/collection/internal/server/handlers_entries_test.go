// Tests for entry CRUD, listing, and bulk update.

package server_test

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	openapi_types "github.com/oapi-codegen/runtime/types"

	"github.com/levonn-dev/vgkeep/libs/go/contract/common"
	"github.com/levonn-dev/vgkeep/libs/go/contract/enrichapi"
	"github.com/levonn-dev/vgkeep/services/collection/internal/enrichmentclient"
	"github.com/levonn-dev/vgkeep/services/collection/internal/store"
)

// consoleProduct is a hardware fixture: a valid proxy pricing target
// that carries no IGDB game identity of its own.
func consoleProduct(id uuid.UUID) enrichapi.Product {
	return enrichapi.Product{
		Id:   id,
		Type: "console",
		Name: "Super NES Console",
	}
}

// ---- creation unit tests ----

func TestUnitCreateEntry_SnapshotsCatalogFacts(t *testing.T) {
	productID := uuid.New()
	var stored store.Entry
	st := &stubStore{createEntry: func(_ context.Context, e store.Entry, tagIDs []uuid.UUID) (store.Entry, error) {
		stored = e
		e.ID = uuid.New()
		r := "n"
		e.BacklogRank = &r
		e.Tags = []store.TagRef{}
		return e, nil
	}}
	var productBearer string
	enrich := &stubEnrichment{
		getProduct: func(_ context.Context, bearer string, id uuid.UUID) (enrichapi.Product, error) {
			productBearer = bearer
			return gameProduct(id), nil
		},
		batchPrices: pricedAs(1500, 4200, 9900),
	}
	c := newStubCache()
	srv, a := newUnitServer(t, st, enrich, c)

	sub := uuid.New()
	resp := do(t, http.MethodPost, srv.URL+"/entries", a.token(t, sub.String()), createBody(productID, nil))
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("status %d", resp.StatusCode)
	}

	// The snapshot came from the product, not the client.
	if stored.UserID != sub || stored.ItemType != "game" || stored.DisplayName != "Chrono Trigger" ||
		stored.PlatformIGDBID == nil || *stored.PlatformIGDBID != 6 ||
		stored.PlatformName == nil || *stored.PlatformName != "SNES" ||
		stored.IGDBGameID == nil || *stored.IGDBGameID != 1000 ||
		stored.FirstReleaseDate == nil ||
		!stored.FirstReleaseDate.Equal(time.Date(1995, time.March, 11, 0, 0, 0, 0, time.UTC)) ||
		stored.MediaType != "physical" || stored.Source != "manual" ||
		stored.ProductID == nil || *stored.ProductID != productID {
		t.Fatalf("snapshot: %+v", stored)
	}
	// The caller's own token rode the enrichment hop.
	if productBearer == "" {
		t.Fatal("bearer must relay to enrichment")
	}
	// The response composed the packaging-matched (cib) value.
	var got struct {
		ValueCents *int64 `json:"value_cents"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&got); err != nil {
		t.Fatal(err)
	}
	if got.ValueCents == nil || *got.ValueCents != 4200 {
		t.Fatalf("value: %v", got.ValueCents)
	}
	// The dashboard cache was invalidated for exactly this user.
	if len(c.invalidated) != 1 || c.invalidated[0] != sub.String() {
		t.Fatalf("invalidations: %v", c.invalidated)
	}
}

func TestUnitCreateEntry_SnapshotsCoverURL(t *testing.T) {
	productID := uuid.New()
	cover := "https://images.igdb.example/chrono-cover.jpg"
	var stored store.Entry
	st := &stubStore{createEntry: func(_ context.Context, e store.Entry, _ []uuid.UUID) (store.Entry, error) {
		stored = e
		e.ID = uuid.New()
		r := "n"
		e.BacklogRank = &r
		e.Tags = []store.TagRef{}
		return e, nil
	}}
	enrich := &stubEnrichment{
		getProduct: func(_ context.Context, _ string, id uuid.UUID) (enrichapi.Product, error) {
			p := gameProduct(id)
			p.Igdb.CoverUrl = &cover
			return p, nil
		},
		batchPrices: pricedAs(1500, 4200, 9900),
	}
	srv, a := newUnitServer(t, st, enrich, newStubCache())

	resp := do(t, http.MethodPost, srv.URL+"/entries", a.token(t, uuid.NewString()), createBody(productID, nil))
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("status %d", resp.StatusCode)
	}
	// The store received the snapshot from the product, not the client.
	if stored.CoverURL == nil || *stored.CoverURL != cover {
		t.Fatalf("stored entry must carry the cover snapshot: %v", stored.CoverURL)
	}
	// The response serializes it too.
	var got struct {
		CoverURL *string `json:"cover_url"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&got); err != nil {
		t.Fatal(err)
	}
	if got.CoverURL == nil || *got.CoverURL != cover {
		t.Fatalf("created entry must carry the cover snapshot: %v", got.CoverURL)
	}
}

// TestUnitCreateEntry_CoverFallsBackToPlatformLogo pins the entry
// image chain: hardware (no igdb block) snapshots the platform logo,
// while a game with real cover art keeps the cover even when a logo
// is also present.
func TestUnitCreateEntry_CoverFallsBackToPlatformLogo(t *testing.T) {
	productID := uuid.New()
	logo := "https://images.igdb.example/t_logo_med/pl7m.jpg"
	cover := "https://images.igdb.example/chrono-cover.jpg"

	var stored store.Entry
	captureStore := func() *stubStore {
		return &stubStore{createEntry: func(_ context.Context, e store.Entry, _ []uuid.UUID) (store.Entry, error) {
			stored = e
			e.ID = uuid.New()
			r := "n"
			e.BacklogRank = &r
			e.Tags = []store.TagRef{}
			return e, nil
		}}
	}

	hardware := &stubEnrichment{
		getProduct: func(_ context.Context, _ string, id uuid.UUID) (enrichapi.Product, error) {
			return enrichapi.Product{
				Id: id, Type: "console", Name: "Gamecube System",
				Platform: &common.PlatformRef{IgdbPlatformId: 21, Name: "Nintendo GameCube", LogoUrl: &logo},
			}, nil
		},
		batchPrices: pricedAs(1500, 4200, 9900),
	}
	srv, a := newUnitServer(t, captureStore(), hardware, newStubCache())
	resp := do(t, http.MethodPost, srv.URL+"/entries", a.token(t, uuid.NewString()), createBody(productID, nil))
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("status %d", resp.StatusCode)
	}
	if stored.CoverURL == nil || *stored.CoverURL != logo {
		t.Fatalf("hardware entry must snapshot the platform logo: %v", stored.CoverURL)
	}

	// A game with cover art keeps it; the logo never overrides.
	game := &stubEnrichment{
		getProduct: func(_ context.Context, _ string, id uuid.UUID) (enrichapi.Product, error) {
			p := gameProduct(id)
			p.Igdb.CoverUrl = &cover
			p.Platform.LogoUrl = &logo
			return p, nil
		},
		batchPrices: pricedAs(1500, 4200, 9900),
	}
	srv2, a2 := newUnitServer(t, captureStore(), game, newStubCache())
	resp2 := do(t, http.MethodPost, srv2.URL+"/entries", a2.token(t, uuid.NewString()), createBody(productID, nil))
	if resp2.StatusCode != http.StatusCreated {
		t.Fatalf("status %d", resp2.StatusCode)
	}
	if stored.CoverURL == nil || *stored.CoverURL != cover {
		t.Fatalf("cover art must win over the platform logo: %v", stored.CoverURL)
	}
}

// Create maps the body field: absent -> auto; user -> user; an
// unknown value answers 400 like pricing_mode does.
func TestCreateEntry_StampsMatchProvenance(t *testing.T) {
	productID := uuid.New()
	newStore := func(stored *store.Entry) *stubStore {
		return &stubStore{createEntry: func(_ context.Context, e store.Entry, _ []uuid.UUID) (store.Entry, error) {
			*stored = e
			e.ID = uuid.New()
			r := "n"
			e.BacklogRank = &r
			e.Tags = []store.TagRef{}
			return e, nil
		}}
	}
	newEnrich := func() *stubEnrichment {
		return &stubEnrichment{
			getProduct: func(_ context.Context, _ string, id uuid.UUID) (enrichapi.Product, error) {
				return gameProduct(id), nil
			},
			batchPrices: pricedAs(1500, 4200, 9900),
		}
	}

	t.Run("absent defaults to auto", func(t *testing.T) {
		var stored store.Entry
		srv, a := newUnitServer(t, newStore(&stored), newEnrich(), newStubCache())
		resp := do(t, http.MethodPost, srv.URL+"/entries", a.token(t, uuid.NewString()), createBody(productID, nil))
		if resp.StatusCode != http.StatusCreated {
			body, _ := io.ReadAll(resp.Body)
			t.Fatalf("status %d: %s", resp.StatusCode, body)
		}
		if stored.MatchProvenance != "auto" {
			t.Fatalf("match_provenance: got %q, want auto", stored.MatchProvenance)
		}
	})

	t.Run("user is stamped as sent", func(t *testing.T) {
		var stored store.Entry
		srv, a := newUnitServer(t, newStore(&stored), newEnrich(), newStubCache())
		resp := do(t, http.MethodPost, srv.URL+"/entries", a.token(t, uuid.NewString()),
			createBody(productID, func(m map[string]any) { m["match_provenance"] = "user" }))
		if resp.StatusCode != http.StatusCreated {
			body, _ := io.ReadAll(resp.Body)
			t.Fatalf("status %d: %s", resp.StatusCode, body)
		}
		if stored.MatchProvenance != "user" {
			t.Fatalf("match_provenance: got %q, want user", stored.MatchProvenance)
		}
	})

	t.Run("unknown value is invalid_body", func(t *testing.T) {
		srv, a := newUnitServer(t, &stubStore{}, &stubEnrichment{}, newStubCache())
		resp := do(t, http.MethodPost, srv.URL+"/entries", a.token(t, uuid.NewString()),
			createBody(productID, func(m map[string]any) { m["match_provenance"] = "auto_ish" }))
		wantProblem(t, resp, http.StatusBadRequest, "invalid_body")
	})
}

func TestUnitCreateEntry_ValidationMatrix(t *testing.T) {
	productID := uuid.New()
	cases := []struct {
		name   string
		mutate func(map[string]any)
		code   string
	}{
		{"empty region", func(m map[string]any) { m["region"] = "   " }, "invalid_body"},
		// 33 multi-byte runes (99 bytes): the cap counts code points,
		// not bytes - see TestCreateEntry_RegionLengthIsRuneCounted for
		// the accept-side 32-rune boundary this pairs with.
		{"region too long", func(m map[string]any) { m["region"] = strings.Repeat("あ", 33) }, "invalid_body"},
		{"bad packaging", func(m map[string]any) { m["packaging"] = "boxed" }, "invalid_body"},
		{"bad status", func(m map[string]any) { m["status"] = "queued" }, "invalid_body"},
		{"digital media", func(m map[string]any) { m["media_type"] = "digital" }, "invalid_body"},
		{"rating over", func(m map[string]any) { m["rating"] = 11 }, "invalid_body"},
		{"negative paid", func(m map[string]any) { m["price_paid_cents"] = -1 }, "invalid_body"},
		{"lowercase currency", func(m map[string]any) { m["currency"] = "usd" }, "invalid_body"},
		{"box grade without box", func(m map[string]any) { m["box_condition"] = "mint" }, "invalid_body"},
		{"manual grade without manual", func(m map[string]any) { m["manual_condition"] = "good" }, "invalid_body"},
		{"proxy without override", func(m map[string]any) { m["pricing_mode"] = "proxy" }, "invalid_body"},
		{"bad condition grade", func(m map[string]any) {
			m["has_box"] = true
			m["box_condition"] = "pristine"
		}, "invalid_body"},
		{"catalog fields with product_id", func(m map[string]any) { m["display_name"] = "hijack" }, "invalid_body"},
		{"custom without display_name", func(m map[string]any) { delete(m, "product_id") }, "invalid_body"},
		{"custom without item_type", func(m map[string]any) {
			delete(m, "product_id")
			m["display_name"] = "Homebrew"
		}, "invalid_body"},
		{"custom with auto pricing", func(m map[string]any) {
			delete(m, "product_id")
			m["display_name"] = "Homebrew"
			m["item_type"] = "game"
			m["pricing_mode"] = "auto"
		}, "invalid_body"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			srv, a := newUnitServer(t, &stubStore{}, &stubEnrichment{}, newStubCache())
			resp := do(t, http.MethodPost, srv.URL+"/entries", a.token(t, uuid.NewString()),
				createBody(productID, tc.mutate))
			wantProblem(t, resp, http.StatusBadRequest, tc.code)
		})
	}
	// Malformed JSON.
	srv, a := newUnitServer(t, &stubStore{}, &stubEnrichment{}, newStubCache())
	resp := do(t, http.MethodPost, srv.URL+"/entries", a.token(t, uuid.NewString()),
		bytes.NewReader([]byte("{")))
	wantProblem(t, resp, http.StatusBadRequest, "invalid_body")
}

// TestCreateEntry_OpenWorldRegion pins the open-world contract: a
// region outside the four known values is not a validation error - it
// is trimmed and stored/returned as an honest display fact.
func TestCreateEntry_OpenWorldRegion(t *testing.T) {
	var stored store.Entry
	st := &stubStore{createEntry: func(_ context.Context, e store.Entry, _ []uuid.UUID) (store.Entry, error) {
		stored = e
		e.ID = uuid.New()
		r := "n"
		e.BacklogRank = &r
		e.Tags = []store.TagRef{}
		return e, nil
	}}
	srv, a := newUnitServer(t, st, &stubEnrichment{}, newStubCache())

	body := jsonBody(map[string]any{
		"display_name": "Import Cart", "item_type": "game",
		"packaging": "loose", "region": "  Korea  ",
	})
	resp := do(t, http.MethodPost, srv.URL+"/entries", a.token(t, uuid.NewString()), body)
	if resp.StatusCode != http.StatusCreated {
		b, _ := io.ReadAll(resp.Body)
		t.Fatalf("status %d: %s", resp.StatusCode, b)
	}
	if stored.Region != "Korea" {
		t.Fatalf("stored region = %q, want trimmed %q", stored.Region, "Korea")
	}
	var got struct {
		Region string `json:"region"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&got); err != nil {
		t.Fatal(err)
	}
	if got.Region != "Korea" {
		t.Fatalf("response region = %q, want trimmed %q", got.Region, "Korea")
	}
}

// TestCreateEntry_CustomCredits pins the custom-entry credit facts:
// names are trimmed, empty elements drop, the arrays store and echo,
// and an absent field stays nil.
func TestCreateEntry_CustomCredits(t *testing.T) {
	var stored store.Entry
	st := &stubStore{createEntry: func(_ context.Context, e store.Entry, _ []uuid.UUID) (store.Entry, error) {
		stored = e
		e.ID = uuid.New()
		e.Tags = []store.TagRef{}
		return e, nil
	}}
	srv, a := newUnitServer(t, st, &stubEnrichment{}, newStubCache())

	body := jsonBody(map[string]any{
		"display_name": "Repro Alpha", "item_type": "game",
		"packaging": "loose", "region": "ntsc_u", "status": "shelved",
		"developers": []string{"  Garage Team  ", "", "Second Studio"},
		"publishers": []string{"Repro House"},
	})
	resp := do(t, http.MethodPost, srv.URL+"/entries", a.token(t, uuid.NewString()), body)
	if resp.StatusCode != http.StatusCreated {
		b, _ := io.ReadAll(resp.Body)
		t.Fatalf("status %d: %s", resp.StatusCode, b)
	}
	if len(stored.Developers) != 2 || stored.Developers[0] != "Garage Team" || stored.Developers[1] != "Second Studio" {
		t.Fatalf("stored developers = %v, want trimmed with the empty element dropped", stored.Developers)
	}
	if len(stored.Publishers) != 1 || stored.Publishers[0] != "Repro House" {
		t.Fatalf("stored publishers = %v", stored.Publishers)
	}
	var got struct {
		Developers []string `json:"developers"`
		Publishers []string `json:"publishers"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&got); err != nil {
		t.Fatal(err)
	}
	if len(got.Developers) != 2 || got.Developers[0] != "Garage Team" {
		t.Fatalf("response developers = %v", got.Developers)
	}

	// Absent fields stay nil (no phantom empty arrays).
	resp = do(t, http.MethodPost, srv.URL+"/entries", a.token(t, uuid.NewString()), jsonBody(map[string]any{
		"display_name": "Plain Cart", "item_type": "game",
		"packaging": "loose", "region": "ntsc_u",
	}))
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("status %d", resp.StatusCode)
	}
	if stored.Developers != nil || stored.Publishers != nil {
		t.Fatalf("absent credit fields must store nil, got %v/%v", stored.Developers, stored.Publishers)
	}
}

// TestCreateEntry_CreditCaps pins the contract's caps on developers/
// publishers (maxItems 10, maxLength 120 per name): more than 10 names
// or a name over 120 runes is a 400, enforced by specval's
// request-validation middleware ahead of the handler, not by
// libs/go/catalogval's NormalizeCredits.
func TestCreateEntry_CreditCaps(t *testing.T) {
	srv, a := newUnitServer(t, &stubStore{}, &stubEnrichment{}, newStubCache())
	base := func(devs []string) io.Reader {
		return jsonBody(map[string]any{
			"display_name": "Repro Alpha", "item_type": "game",
			"packaging": "loose", "region": "ntsc_u",
			"developers": devs,
		})
	}

	eleven := make([]string, 11)
	for i := range eleven {
		eleven[i] = fmt.Sprintf("Studio %d", i)
	}
	resp := do(t, http.MethodPost, srv.URL+"/entries", a.token(t, uuid.NewString()), base(eleven))
	wantProblem(t, resp, http.StatusBadRequest, "invalid_body")

	long := strings.Repeat("x", 121)
	resp = do(t, http.MethodPost, srv.URL+"/entries", a.token(t, uuid.NewString()), base([]string{long}))
	wantProblem(t, resp, http.StatusBadRequest, "invalid_body")
}

// TestCreateEntry_RegionLengthIsRuneCounted pins the 32-char cap as
// code points, matching every other length cap in this file
// (display_name, platform_name, storage_location all use
// utf8.RuneCountInString, never len()). 32 multi-byte runes is 96
// bytes - over 32 by byte count - and must still be accepted; the
// ValidationMatrix's "region too long" row pins the reject side of
// this same boundary at 33 runes.
func TestCreateEntry_RegionLengthIsRuneCounted(t *testing.T) {
	region32 := strings.Repeat("あ", 32)
	var stored store.Entry
	st := &stubStore{createEntry: func(_ context.Context, e store.Entry, _ []uuid.UUID) (store.Entry, error) {
		stored = e
		e.ID = uuid.New()
		r := "n"
		e.BacklogRank = &r
		e.Tags = []store.TagRef{}
		return e, nil
	}}
	srv, a := newUnitServer(t, st, &stubEnrichment{}, newStubCache())

	body := jsonBody(map[string]any{
		"display_name": "Import Cart", "item_type": "game",
		"packaging": "loose", "region": region32,
	})
	resp := do(t, http.MethodPost, srv.URL+"/entries", a.token(t, uuid.NewString()), body)
	if resp.StatusCode != http.StatusCreated {
		b, _ := io.ReadAll(resp.Body)
		t.Fatalf("status %d: %s (32 runes = %d bytes, must be accepted)", resp.StatusCode, b, len(region32))
	}
	if stored.Region != region32 {
		t.Fatalf("stored region = %q, want %q", stored.Region, region32)
	}
}

func TestUnitCreateEntry_ReferenceErrors(t *testing.T) {
	productID := uuid.New()

	t.Run("unknown product is 404", func(t *testing.T) {
		enrich := &stubEnrichment{getProduct: func(context.Context, string, uuid.UUID) (enrichapi.Product, error) {
			return enrichapi.Product{}, enrichmentclient.ErrUnknownProduct
		}}
		srv, a := newUnitServer(t, &stubStore{}, enrich, newStubCache())
		resp := do(t, http.MethodPost, srv.URL+"/entries", a.token(t, uuid.NewString()), createBody(productID, nil))
		wantProblem(t, resp, http.StatusNotFound, "unknown_product")
	})

	t.Run("enrichment down is 502", func(t *testing.T) {
		enrich := &stubEnrichment{getProduct: func(context.Context, string, uuid.UUID) (enrichapi.Product, error) {
			return enrichapi.Product{}, enrichmentclient.ErrUnavailable
		}}
		srv, a := newUnitServer(t, &stubStore{}, enrich, newStubCache())
		resp := do(t, http.MethodPost, srv.URL+"/entries", a.token(t, uuid.NewString()), createBody(productID, nil))
		wantProblem(t, resp, http.StatusBadGateway, "enrichment_unavailable")
	})

	t.Run("unknown proxy product is 404", func(t *testing.T) {
		proxyID := uuid.New()
		enrich := &stubEnrichment{getProduct: func(_ context.Context, _ string, id uuid.UUID) (enrichapi.Product, error) {
			if id == proxyID {
				return enrichapi.Product{}, enrichmentclient.ErrUnknownProduct
			}
			return gameProduct(id), nil
		}}
		srv, a := newUnitServer(t, &stubStore{}, enrich, newStubCache())
		resp := do(t, http.MethodPost, srv.URL+"/entries", a.token(t, uuid.NewString()),
			createBody(productID, func(m map[string]any) {
				m["pricing_mode"] = "proxy"
				m["pricing_product_id"] = proxyID.String()
			}))
		wantProblem(t, resp, http.StatusNotFound, "unknown_pricing_product")
	})

	t.Run("proxy product unavailable is 502", func(t *testing.T) {
		proxyID := uuid.New()
		enrich := &stubEnrichment{getProduct: func(_ context.Context, _ string, id uuid.UUID) (enrichapi.Product, error) {
			if id == proxyID {
				return enrichapi.Product{}, enrichmentclient.ErrUnavailable
			}
			return gameProduct(id), nil
		}}
		srv, a := newUnitServer(t, &stubStore{}, enrich, newStubCache())
		resp := do(t, http.MethodPost, srv.URL+"/entries", a.token(t, uuid.NewString()),
			createBody(productID, func(m map[string]any) {
				m["pricing_mode"] = "proxy"
				m["pricing_product_id"] = proxyID.String()
			}))
		wantProblem(t, resp, http.StatusBadGateway, "enrichment_unavailable")
	})

	t.Run("unknown tag is 404", func(t *testing.T) {
		st := &stubStore{createEntry: func(context.Context, store.Entry, []uuid.UUID) (store.Entry, error) {
			return store.Entry{}, store.ErrTagNotFound
		}}
		enrich := &stubEnrichment{getProduct: func(_ context.Context, _ string, id uuid.UUID) (enrichapi.Product, error) {
			return gameProduct(id), nil
		}}
		srv, a := newUnitServer(t, st, enrich, newStubCache())
		resp := do(t, http.MethodPost, srv.URL+"/entries", a.token(t, uuid.NewString()),
			createBody(productID, func(m map[string]any) { m["tag_ids"] = []string{uuid.NewString()} }))
		wantProblem(t, resp, http.StatusNotFound, "tag_not_found")
	})

	t.Run("custom entry needs no enrichment at all", func(t *testing.T) {
		var stored store.Entry
		st := &stubStore{createEntry: func(_ context.Context, e store.Entry, _ []uuid.UUID) (store.Entry, error) {
			stored = e
			e.ID = uuid.New()
			r := "n"
			e.BacklogRank = &r
			e.Tags = []store.TagRef{}
			return e, nil
		}}
		// Both enrichment fields deliberately nil: a call would panic.
		// A custom create with disabled pricing works with the catalog
		// fully down.
		srv, a := newUnitServer(t, st, &stubEnrichment{}, newStubCache())
		resp := do(t, http.MethodPost, srv.URL+"/entries", a.token(t, uuid.NewString()),
			jsonBody(map[string]any{
				"display_name":       "Chrono Trigger (fan translation cart)",
				"item_type":          "game",
				"platform_name":      "SNES",
				"first_release_date": "1995-03-11",
				"region":             "ntsc_u",
				"packaging":          "loose",
			}))
		if resp.StatusCode != http.StatusCreated {
			t.Fatalf("status %d", resp.StatusCode)
		}
		if stored.ProductID != nil || stored.ItemType != "game" ||
			stored.DisplayName != "Chrono Trigger (fan translation cart)" ||
			stored.PlatformName == nil || *stored.PlatformName != "SNES" ||
			stored.PlatformIGDBID != nil || stored.IGDBGameID != nil ||
			stored.PricingMode != "disabled" {
			t.Fatalf("custom entry: %+v", stored)
		}
		var got struct {
			ProductID  *uuid.UUID `json:"product_id"`
			ValueCents *int64     `json:"value_cents"`
		}
		_ = json.NewDecoder(resp.Body).Decode(&got)
		if got.ProductID != nil || got.ValueCents != nil {
			t.Fatalf("custom response: %+v", got)
		}
	})

	t.Run("custom with proxy validates the target, prices from it, and adopts its identity", func(t *testing.T) {
		proxyID := uuid.New()
		var productCalls []uuid.UUID
		var stored store.Entry
		st := &stubStore{createEntry: func(_ context.Context, e store.Entry, _ []uuid.UUID) (store.Entry, error) {
			stored = e
			e.ID = uuid.New()
			r := "n"
			e.BacklogRank = &r
			e.Tags = []store.TagRef{}
			return e, nil
		}}
		enrich := &stubEnrichment{
			getProduct: func(_ context.Context, _ string, id uuid.UUID) (enrichapi.Product, error) {
				productCalls = append(productCalls, id)
				return gameProduct(id), nil
			},
			batchPrices: pricedAs(1500, 4200, 9900),
		}
		srv, a := newUnitServer(t, st, enrich, newStubCache())
		resp := do(t, http.MethodPost, srv.URL+"/entries", a.token(t, uuid.NewString()),
			jsonBody(map[string]any{
				"display_name": "Custom repro", "item_type": "game",
				"region": "ntsc_u", "packaging": "loose",
				"pricing_mode": "proxy", "pricing_product_id": proxyID.String(),
			}))
		if resp.StatusCode != http.StatusCreated {
			t.Fatalf("status %d", resp.StatusCode)
		}
		if len(productCalls) != 1 || productCalls[0] != proxyID {
			t.Fatalf("proxy target must be validated exactly once: %v", productCalls)
		}
		// The recommendation identity came from the proxy target.
		if stored.IGDBGameID == nil || *stored.IGDBGameID != 1000 {
			t.Fatalf("proxy identity: %v", stored.IGDBGameID)
		}
		var got struct {
			ValueCents *int64 `json:"value_cents"`
		}
		_ = json.NewDecoder(resp.Body).Decode(&got)
		if got.ValueCents == nil || *got.ValueCents != 1500 { // loose packaging via the proxy
			t.Fatalf("proxied value: %v", got.ValueCents)
		}
	})

	t.Run("value composition failure still creates", func(t *testing.T) {
		st := &stubStore{createEntry: func(_ context.Context, e store.Entry, _ []uuid.UUID) (store.Entry, error) {
			e.ID = uuid.New()
			r := "n"
			e.BacklogRank = &r
			e.Tags = []store.TagRef{}
			return e, nil
		}}
		enrich := &stubEnrichment{
			getProduct: func(_ context.Context, _ string, id uuid.UUID) (enrichapi.Product, error) {
				return gameProduct(id), nil
			},
			batchPrices: func(context.Context, string, []uuid.UUID) (map[string]enrichapi.ProductPrices, error) {
				return nil, enrichmentclient.ErrUnavailable
			},
		}
		srv, a := newUnitServer(t, st, enrich, newStubCache())
		resp := do(t, http.MethodPost, srv.URL+"/entries", a.token(t, uuid.NewString()), createBody(productID, nil))
		if resp.StatusCode != http.StatusCreated {
			t.Fatalf("status %d", resp.StatusCode)
		}
		var got struct {
			ValueCents *int64 `json:"value_cents"`
		}
		_ = json.NewDecoder(resp.Body).Decode(&got)
		if got.ValueCents != nil {
			t.Fatal("value must be null when pricing is unavailable")
		}
	})
}

// TestUnitCreateEntry_HardwareProxyGrantsNoGameIdentity covers a custom
// game proxied to a hardware product: the proxy prices the entry, but
// hardware carries no IGDB game identity to adopt (contrast the
// proxy-to-a-game case in TestUnitCreateEntry_ReferenceErrors above,
// which does adopt the target's identity).
func TestUnitCreateEntry_HardwareProxyGrantsNoGameIdentity(t *testing.T) {
	consoleID := uuid.New()
	var stored store.Entry
	st := &stubStore{createEntry: func(_ context.Context, e store.Entry, _ []uuid.UUID) (store.Entry, error) {
		stored = e
		e.ID = uuid.New()
		r := "n"
		e.BacklogRank = &r
		e.Tags = []store.TagRef{}
		return e, nil
	}}
	enrich := &stubEnrichment{
		getProduct: func(_ context.Context, _ string, id uuid.UUID) (enrichapi.Product, error) {
			return consoleProduct(id), nil
		},
		batchPrices: pricedAs(1500, 4200, 9900),
	}
	srv, a := newUnitServer(t, st, enrich, newStubCache())
	resp := do(t, http.MethodPost, srv.URL+"/entries", a.token(t, uuid.NewString()),
		jsonBody(map[string]any{
			"display_name": "Homebrew cart", "item_type": "game",
			"region": "ntsc_u", "packaging": "loose",
			"pricing_mode": "proxy", "pricing_product_id": consoleID.String(),
		}))
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("status %d", resp.StatusCode)
	}
	if stored.IGDBGameID != nil {
		t.Fatalf("hardware proxy target must grant no game identity: %v", *stored.IGDBGameID)
	}
}

// TestUnitCreateEntry_SnapshotsRegionScopedReleaseDate pins the create
// snapshot to the region-scoped pick over the platform scalar: a
// chain hit for the entry's region wins, and a region with no chain
// (region_free) falls back to the scalar exactly like a product with
// no per-region dates at all.
func TestUnitCreateEntry_SnapshotsRegionScopedReleaseDate(t *testing.T) {
	productID := uuid.New()
	dated := func(id uuid.UUID) enrichapi.Product {
		p := gameProduct(id)
		// The scalar is deliberately distinct from every row date below,
		// so a test asserting the scalar (region_free) cannot pass by
		// accident on a row's date instead.
		scalar := openapi_types.Date{Time: time.Date(1994, time.December, 25, 0, 0, 0, 0, time.UTC)}
		p.Igdb.FirstReleaseDate = &scalar
		p.Igdb.ReleaseDates = &[]common.ReleaseDate{
			{Region: "japan", Date: openapi_types.Date{Time: time.Date(1995, time.March, 11, 0, 0, 0, 0, time.UTC)}},
			{Region: "north_america", Date: openapi_types.Date{Time: time.Date(1995, time.August, 22, 0, 0, 0, 0, time.UTC)}},
		}
		return p
	}
	newStore := func(stored *store.Entry) *stubStore {
		return &stubStore{createEntry: func(_ context.Context, e store.Entry, _ []uuid.UUID) (store.Entry, error) {
			*stored = e
			e.ID = uuid.New()
			r := "n"
			e.BacklogRank = &r
			e.Tags = []store.TagRef{}
			return e, nil
		}}
	}

	t.Run("chain hit for the entry's region wins over the scalar", func(t *testing.T) {
		var stored store.Entry
		enrich := &stubEnrichment{
			getProduct:  func(_ context.Context, _ string, id uuid.UUID) (enrichapi.Product, error) { return dated(id), nil },
			batchPrices: pricedAs(1500, 4200, 9900),
		}
		srv, a := newUnitServer(t, newStore(&stored), enrich, newStubCache())
		resp := do(t, http.MethodPost, srv.URL+"/entries", a.token(t, uuid.NewString()), createBody(productID, nil)) // region ntsc_u
		if resp.StatusCode != http.StatusCreated {
			t.Fatalf("status %d", resp.StatusCode)
		}
		want := time.Date(1995, time.August, 22, 0, 0, 0, 0, time.UTC)
		if stored.FirstReleaseDate == nil || !stored.FirstReleaseDate.Equal(want) {
			t.Fatalf("region-scoped pick: %v", stored.FirstReleaseDate)
		}
	})

	t.Run("region_free has no chain, falls back to the scalar", func(t *testing.T) {
		var stored store.Entry
		enrich := &stubEnrichment{
			getProduct:  func(_ context.Context, _ string, id uuid.UUID) (enrichapi.Product, error) { return dated(id), nil },
			batchPrices: pricedAs(1500, 4200, 9900),
		}
		srv, a := newUnitServer(t, newStore(&stored), enrich, newStubCache())
		resp := do(t, http.MethodPost, srv.URL+"/entries", a.token(t, uuid.NewString()),
			createBody(productID, func(m map[string]any) { m["region"] = "region_free" }))
		if resp.StatusCode != http.StatusCreated {
			t.Fatalf("status %d", resp.StatusCode)
		}
		want := time.Date(1994, time.December, 25, 0, 0, 0, 0, time.UTC) // the scalar; region_free chains nowhere
		if stored.FirstReleaseDate == nil || !stored.FirstReleaseDate.Equal(want) {
			t.Fatalf("scalar fallback: %v", stored.FirstReleaseDate)
		}
	})
}

// TestUnitCreateEntry_SnapshotsRegionPickedLocalization pins the
// create-time localized snapshot: an ntsc_j entry against a product
// carrying a ja-JP bundle stores and returns the whole trio, while
// the same product under ntsc_u (a region with no localization chain)
// stores nothing and serializes nothing - the client never supplies
// these, so an absent field is the only way to say "no localized
// form".
func TestUnitCreateEntry_SnapshotsRegionPickedLocalization(t *testing.T) {
	productID := uuid.New()
	newStore := func(stored *store.Entry) *stubStore {
		return &stubStore{createEntry: func(_ context.Context, e store.Entry, _ []uuid.UUID) (store.Entry, error) {
			*stored = e
			e.ID = uuid.New()
			r := "n"
			e.BacklogRank = &r
			e.Tags = []store.TagRef{}
			return e, nil
		}}
	}
	newEnrich := func() *stubEnrichment {
		return &stubEnrichment{
			getProduct: func(_ context.Context, _ string, id uuid.UUID) (enrichapi.Product, error) {
				return localizedGameProduct(id), nil
			},
			batchPrices: pricedAs(1500, 4200, 9900),
		}
	}

	t.Run("ntsc_j snapshots the ja-JP bundle", func(t *testing.T) {
		var stored store.Entry
		srv, a := newUnitServer(t, newStore(&stored), newEnrich(), newStubCache())
		resp := do(t, http.MethodPost, srv.URL+"/entries", a.token(t, uuid.NewString()),
			createBody(productID, func(m map[string]any) { m["region"] = "ntsc_j" }))
		if resp.StatusCode != http.StatusCreated {
			t.Fatalf("status %d", resp.StatusCode)
		}
		if stored.LocalizedName == nil || *stored.LocalizedName != "聖剣伝説3" ||
			stored.LocalizedNameTranslit == nil || *stored.LocalizedNameTranslit != "Seiken Densetsu 3" ||
			stored.LocalizedCoverURL == nil || *stored.LocalizedCoverURL != "https://images.igdb.example/jp.jpg" {
			t.Fatalf("stored localized snapshot: %v %v %v",
				stored.LocalizedName, stored.LocalizedNameTranslit, stored.LocalizedCoverURL)
		}
		// The canonical snapshot is untouched: the localized fields are
		// an addition to the display name and cover, not a replacement.
		if stored.DisplayName != "Chrono Trigger" {
			t.Fatalf("display_name must stay canonical: %q", stored.DisplayName)
		}
		var got struct {
			LocalizedName         *string `json:"localized_name"`
			LocalizedNameTranslit *string `json:"localized_name_translit"`
			LocalizedCoverURL     *string `json:"localized_cover_url"`
		}
		if err := json.NewDecoder(resp.Body).Decode(&got); err != nil {
			t.Fatal(err)
		}
		if got.LocalizedName == nil || *got.LocalizedName != "聖剣伝説3" ||
			got.LocalizedNameTranslit == nil || *got.LocalizedNameTranslit != "Seiken Densetsu 3" ||
			got.LocalizedCoverURL == nil || *got.LocalizedCoverURL != "https://images.igdb.example/jp.jpg" {
			t.Fatalf("response localized snapshot: %v %v %v",
				got.LocalizedName, got.LocalizedNameTranslit, got.LocalizedCoverURL)
		}
	})

	t.Run("ntsc_u has no chain: nothing stored, nothing serialized", func(t *testing.T) {
		var stored store.Entry
		srv, a := newUnitServer(t, newStore(&stored), newEnrich(), newStubCache())
		resp := do(t, http.MethodPost, srv.URL+"/entries", a.token(t, uuid.NewString()),
			createBody(productID, nil)) // region ntsc_u
		if resp.StatusCode != http.StatusCreated {
			t.Fatalf("status %d", resp.StatusCode)
		}
		if stored.LocalizedName != nil || stored.LocalizedNameTranslit != nil || stored.LocalizedCoverURL != nil {
			t.Fatalf("ntsc_u must snapshot no localized form: %v %v %v",
				stored.LocalizedName, stored.LocalizedNameTranslit, stored.LocalizedCoverURL)
		}
		raw, _ := io.ReadAll(resp.Body)
		if strings.Contains(string(raw), "localized_") {
			t.Fatalf("absent fields must not serialize: %s", raw)
		}
	})
}

func TestCreateEntryPersistsThroughTheStack(t *testing.T) {
	s := newStack(t)
	productID := s.enrich.addGame("Chrono Trigger", 1500, 4200, 9900)
	sub := uuid.New()
	tok := s.auth.token(t, sub.String())

	// Seed the caller's dashboard cache; creation must invalidate it in
	// the real Valkey instance, not just in an in-memory stub.
	if err := s.cache.PutDashboard(context.Background(), sub.String(), []byte(`{"seed":true}`), 5*time.Minute); err != nil {
		t.Fatal(err)
	}

	resp := do(t, http.MethodPost, s.baseURL+"/entries", tok, createBody(productID, nil))
	if resp.StatusCode != http.StatusCreated {
		body, _ := io.ReadAll(resp.Body)
		t.Fatalf("status %d: %s", resp.StatusCode, body)
	}
	var got struct {
		ID          uuid.UUID `json:"id"`
		DisplayName string    `json:"display_name"`
		BacklogRank *string   `json:"backlog_rank"`
		ValueCents  *int64    `json:"value_cents"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&got); err != nil {
		t.Fatal(err)
	}
	if got.DisplayName != "Chrono Trigger" || got.BacklogRank == nil ||
		got.ValueCents == nil || *got.ValueCents != 4200 {
		t.Fatalf("created: %+v", got)
	}
	// The row is real: read it back through the store.
	stored, err := s.store.GetEntry(context.Background(), sub, got.ID)
	if err != nil || stored.IGDBGameID == nil || *stored.IGDBGameID != 1000 {
		t.Fatalf("persisted row: %+v %v", stored, err)
	}
	// The seeded dashboard entry is gone: creation invalidated it for real.
	if cached, err := s.cache.GetDashboard(context.Background(), sub.String()); err != nil || cached != nil {
		t.Fatalf("dashboard cache: got (%v, %v), want a miss", cached, err)
	}
	// Owning two copies is legitimate: same product, second entry.
	resp = do(t, http.MethodPost, s.baseURL+"/entries", tok, createBody(productID, nil))
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("second copy: %d", resp.StatusCode)
	}
}

// storedGameEntry is a persisted-looking product-backed entry for
// stub reads.
func storedGameEntry(userID uuid.UUID) store.Entry {
	rank := "n"
	return store.Entry{
		ID: uuid.New(), UserID: userID, ProductID: new(uuid.New()),
		ItemType: "game", MediaType: "physical", DisplayName: "Chrono Trigger",
		Region: "ntsc_u", Packaging: "cib", Currency: "USD",
		PricingMode: "auto", MatchProvenance: "auto", Status: "backlog", BacklogRank: &rank,
		Source: "manual", Tags: []store.TagRef{},
	}
}

// updateBody is a minimal valid full-replacement body.
func updateBody(mutate func(map[string]any)) *bytes.Reader {
	m := map[string]any{
		"region":       "ntsc_u",
		"packaging":    "loose",
		"pricing_mode": "auto",
		"status":       "beaten",
		"pinned":       false,
	}
	if mutate != nil {
		mutate(m)
	}
	b, _ := json.Marshal(m)
	return bytes.NewReader(b)
}

func TestUnitGetEntry(t *testing.T) {
	user := uuid.New()
	e := storedGameEntry(user)
	st := &stubStore{getEntry: func(_ context.Context, gotUser, gotID uuid.UUID) (store.Entry, error) {
		if gotUser != user || gotID != e.ID {
			return store.Entry{}, store.ErrNotFound
		}
		return e, nil
	}}
	enrich := &stubEnrichment{batchPrices: pricedAs(1500, 4200, 9900)}
	srv, a := newUnitServer(t, st, enrich, newStubCache())

	resp := do(t, http.MethodGet, srv.URL+"/entries/"+e.ID.String(), a.token(t, user.String()), nil)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status %d", resp.StatusCode)
	}
	var got struct {
		ValueCents *int64 `json:"value_cents"`
	}
	_ = json.NewDecoder(resp.Body).Decode(&got)
	if got.ValueCents == nil || *got.ValueCents != 4200 {
		t.Fatalf("cib value: %v", got.ValueCents)
	}

	// A foreign caller gets 404, not 403: no existence leak.
	resp = do(t, http.MethodGet, srv.URL+"/entries/"+e.ID.String(), a.token(t, uuid.NewString()), nil)
	wantProblem(t, resp, http.StatusNotFound, "entry_not_found")
}

func TestUnitGetEntry_DisabledPricingSkipsComposition(t *testing.T) {
	user := uuid.New()
	e := storedGameEntry(user)
	e.PricingMode = "disabled"
	st := &stubStore{getEntry: func(context.Context, uuid.UUID, uuid.UUID) (store.Entry, error) { return e, nil }}
	// batchPrices deliberately nil: a call would panic the stub.
	srv, a := newUnitServer(t, st, &stubEnrichment{}, newStubCache())
	resp := do(t, http.MethodGet, srv.URL+"/entries/"+e.ID.String(), a.token(t, user.String()), nil)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status %d", resp.StatusCode)
	}
	var got struct {
		ValueCents *int64 `json:"value_cents"`
	}
	_ = json.NewDecoder(resp.Body).Decode(&got)
	if got.ValueCents != nil {
		t.Fatal("disabled pricing must not compose a value")
	}
}

// TestUnitGetEntry_SealedPricesFromNewCents pins that packaging=sealed
// composes the new-price quote, not the loose or cib figure.
func TestUnitGetEntry_SealedPricesFromNewCents(t *testing.T) {
	user := uuid.New()
	e := storedGameEntry(user)
	e.Packaging = "sealed"
	st := &stubStore{getEntry: func(context.Context, uuid.UUID, uuid.UUID) (store.Entry, error) { return e, nil }}
	enrich := &stubEnrichment{batchPrices: pricedAs(1500, 4200, 9900)}
	srv, a := newUnitServer(t, st, enrich, newStubCache())

	resp := do(t, http.MethodGet, srv.URL+"/entries/"+e.ID.String(), a.token(t, user.String()), nil)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status %d", resp.StatusCode)
	}
	var got struct {
		ValueCents *int64 `json:"value_cents"`
	}
	_ = json.NewDecoder(resp.Body).Decode(&got)
	if got.ValueCents == nil || *got.ValueCents != 9900 {
		t.Fatalf("sealed packaging must price from new_cents (9900), got %v", got.ValueCents)
	}
}

func TestUnitUpdateEntry(t *testing.T) {
	user := uuid.New()
	e := storedGameEntry(user)

	t.Run("happy replace preserves identity, invalidates dashboard", func(t *testing.T) {
		var updated store.Entry
		st := &stubStore{
			getEntry: func(context.Context, uuid.UUID, uuid.UUID) (store.Entry, error) { return e, nil },
			updateEntry: func(_ context.Context, in store.Entry, _ []uuid.UUID) (store.Entry, error) {
				updated = in
				in.Tags = []store.TagRef{}
				return in, nil
			},
		}
		enrich := &stubEnrichment{batchPrices: pricedAs(1500, 4200, 9900)}
		c := newStubCache()
		srv, a := newUnitServer(t, st, enrich, c)
		resp := do(t, http.MethodPut, srv.URL+"/entries/"+e.ID.String(), a.token(t, user.String()),
			updateBody(func(m map[string]any) { m["rating"] = 8 }))
		if resp.StatusCode != http.StatusOK {
			t.Fatalf("status %d", resp.StatusCode)
		}
		// Identity + snapshot survive; mutables replaced; rating set;
		// absent notes cleared.
		if updated.ProductID == nil || *updated.ProductID != *e.ProductID ||
			updated.DisplayName != e.DisplayName ||
			updated.Status != "beaten" || updated.Packaging != "loose" ||
			updated.Rating == nil || *updated.Rating != 8 || updated.Notes != nil {
			t.Fatalf("update payload: %+v", updated)
		}
		if len(c.invalidated) != 1 {
			t.Fatalf("invalidations: %v", c.invalidated)
		}
	})

	t.Run("validation failures answer 400", func(t *testing.T) {
		st := &stubStore{getEntry: func(context.Context, uuid.UUID, uuid.UUID) (store.Entry, error) { return e, nil }}
		srv, a := newUnitServer(t, st, &stubEnrichment{}, newStubCache())
		resp := do(t, http.MethodPut, srv.URL+"/entries/"+e.ID.String(), a.token(t, user.String()),
			updateBody(func(m map[string]any) { m["status"] = "queued" }))
		wantProblem(t, resp, http.StatusBadRequest, "invalid_body")
	})

	t.Run("a NEW proxy reference is validated, an unchanged one is not", func(t *testing.T) {
		proxied := e
		known := uuid.New()
		proxied.PricingMode = "proxy"
		proxied.PricingProductID = &known
		var productCalls int
		st := &stubStore{
			getEntry: func(context.Context, uuid.UUID, uuid.UUID) (store.Entry, error) { return proxied, nil },
			updateEntry: func(_ context.Context, in store.Entry, _ []uuid.UUID) (store.Entry, error) {
				in.Tags = []store.TagRef{}
				return in, nil
			},
		}
		enrich := &stubEnrichment{
			getProduct: func(_ context.Context, _ string, id uuid.UUID) (enrichapi.Product, error) {
				productCalls++
				return enrichapi.Product{}, enrichmentclient.ErrUnknownProduct
			},
			batchPrices: pricedAs(1, 2, 3),
		}
		srv, a := newUnitServer(t, st, enrich, newStubCache())

		// Unchanged reference: no enrichment validation call.
		resp := do(t, http.MethodPut, srv.URL+"/entries/"+proxied.ID.String(), a.token(t, user.String()),
			updateBody(func(m map[string]any) {
				m["pricing_mode"] = "proxy"
				m["pricing_product_id"] = known.String()
			}))
		if resp.StatusCode != http.StatusOK || productCalls != 0 {
			t.Fatalf("unchanged proxy: status %d, calls %d", resp.StatusCode, productCalls)
		}
		// A new, unknown reference: 404.
		resp = do(t, http.MethodPut, srv.URL+"/entries/"+proxied.ID.String(), a.token(t, user.String()),
			updateBody(func(m map[string]any) {
				m["pricing_mode"] = "proxy"
				m["pricing_product_id"] = uuid.NewString()
			}))
		wantProblem(t, resp, http.StatusNotFound, "unknown_pricing_product")
	})

	// The gate's other disjunct: a product-backed entry proxying to its
	// OWN product (rather than an unchanged proxy override) is also
	// already known-good and needs no round-trip.
	t.Run("proxying to the entry's own product needs no validation", func(t *testing.T) {
		var updated store.Entry
		st := &stubStore{
			getEntry: func(context.Context, uuid.UUID, uuid.UUID) (store.Entry, error) { return e, nil },
			updateEntry: func(_ context.Context, in store.Entry, _ []uuid.UUID) (store.Entry, error) {
				updated = in
				in.Tags = []store.TagRef{}
				return in, nil
			},
		}
		enrich := &stubEnrichment{
			// getProduct deliberately nil: a call would panic the stub.
			batchPrices: pricedAs(1500, 4200, 9900),
		}
		srv, a := newUnitServer(t, st, enrich, newStubCache())

		resp := do(t, http.MethodPut, srv.URL+"/entries/"+e.ID.String(), a.token(t, user.String()),
			updateBody(func(m map[string]any) {
				m["pricing_mode"] = "proxy"
				m["pricing_product_id"] = e.ProductID.String()
			}))
		if resp.StatusCode != http.StatusOK {
			body, _ := io.ReadAll(resp.Body)
			t.Fatalf("status %d: %s", resp.StatusCode, body)
		}
		if updated.PricingMode != "proxy" || updated.PricingProductID == nil || *updated.PricingProductID != *e.ProductID {
			t.Fatalf("update payload: %+v", updated)
		}
	})

	t.Run("switching disabled to proxy re-validates even if pricing_product_id was already stored", func(t *testing.T) {
		// The column persists regardless of mode, so a prior PUT could
		// have stashed pricing_product_id under mode "disabled" without
		// ever validating it. Comparing only against the raw stored
		// column (not the stored mode) would let that stale, unvalidated
		// id become the active proxy target the moment mode flips to
		// "proxy" - the bug this test guards against.
		stale := e
		stale.PricingMode = "disabled"
		unknown := uuid.New()
		stale.PricingProductID = &unknown
		var productCalls int
		st := &stubStore{
			getEntry: func(context.Context, uuid.UUID, uuid.UUID) (store.Entry, error) { return stale, nil },
			updateEntry: func(_ context.Context, in store.Entry, _ []uuid.UUID) (store.Entry, error) {
				in.Tags = []store.TagRef{}
				return in, nil
			},
		}
		enrich := &stubEnrichment{
			getProduct: func(_ context.Context, _ string, id uuid.UUID) (enrichapi.Product, error) {
				productCalls++
				return enrichapi.Product{}, enrichmentclient.ErrUnknownProduct
			},
			batchPrices: pricedAs(1, 2, 3),
		}
		srv, a := newUnitServer(t, st, enrich, newStubCache())

		resp := do(t, http.MethodPut, srv.URL+"/entries/"+stale.ID.String(), a.token(t, user.String()),
			updateBody(func(m map[string]any) {
				m["pricing_mode"] = "proxy"
				m["pricing_product_id"] = unknown.String()
			}))
		wantProblem(t, resp, http.StatusNotFound, "unknown_pricing_product")
		if productCalls != 1 {
			t.Fatalf("switching into proxy mode must validate: calls %d", productCalls)
		}
	})

	t.Run("re-putting an active proxy at the same target skips re-validation and keeps identity", func(t *testing.T) {
		owner := uuid.New()
		target := uuid.New()
		cust := storedGameEntry(owner)
		cust.ProductID = nil
		cust.PricingMode = "proxy"
		cust.PricingProductID = &target
		cust.IGDBGameID = new(int64(1000))
		var productCalls int
		var updated store.Entry
		st := &stubStore{
			getEntry: func(context.Context, uuid.UUID, uuid.UUID) (store.Entry, error) { return cust, nil },
			updateEntry: func(_ context.Context, in store.Entry, _ []uuid.UUID) (store.Entry, error) {
				updated = in
				in.Tags = []store.TagRef{}
				return in, nil
			},
		}
		enrich := &stubEnrichment{
			getProduct: func(_ context.Context, _ string, id uuid.UUID) (enrichapi.Product, error) {
				productCalls++
				return gameProduct(id), nil
			},
			batchPrices: pricedAs(1500, 4200, 9900),
		}
		srv, a := newUnitServer(t, st, enrich, newStubCache())

		resp := do(t, http.MethodPut, srv.URL+"/entries/"+cust.ID.String(), a.token(t, owner.String()),
			updateBody(func(m map[string]any) {
				m["pricing_mode"] = "proxy"
				m["pricing_product_id"] = target.String()
				m["display_name"] = cust.DisplayName
			}))
		if resp.StatusCode != http.StatusOK {
			t.Fatalf("status %d", resp.StatusCode)
		}
		if productCalls != 0 {
			t.Fatalf("unchanged active proxy must not re-validate: calls %d", productCalls)
		}
		if updated.IGDBGameID == nil || *updated.IGDBGameID != 1000 {
			t.Fatalf("identity must be kept unchanged: %v", updated.IGDBGameID)
		}
	})

	t.Run("switching disabled to proxy at a real target re-validates and adopts identity", func(t *testing.T) {
		owner := uuid.New()
		target := uuid.New()
		cust := storedGameEntry(owner)
		cust.ProductID = nil
		cust.PricingMode = "disabled"
		cust.PricingProductID = &target
		cust.IGDBGameID = nil
		var productCalls int
		var updated store.Entry
		st := &stubStore{
			getEntry: func(context.Context, uuid.UUID, uuid.UUID) (store.Entry, error) { return cust, nil },
			updateEntry: func(_ context.Context, in store.Entry, _ []uuid.UUID) (store.Entry, error) {
				updated = in
				in.Tags = []store.TagRef{}
				return in, nil
			},
		}
		enrich := &stubEnrichment{
			getProduct: func(_ context.Context, _ string, id uuid.UUID) (enrichapi.Product, error) {
				productCalls++
				return gameProduct(id), nil
			},
			batchPrices: pricedAs(1500, 4200, 9900),
		}
		srv, a := newUnitServer(t, st, enrich, newStubCache())

		resp := do(t, http.MethodPut, srv.URL+"/entries/"+cust.ID.String(), a.token(t, owner.String()),
			updateBody(func(m map[string]any) {
				m["pricing_mode"] = "proxy"
				m["pricing_product_id"] = target.String()
				m["display_name"] = cust.DisplayName
			}))
		if resp.StatusCode != http.StatusOK {
			t.Fatalf("status %d", resp.StatusCode)
		}
		if productCalls != 1 {
			t.Fatalf("disabled-to-proxy must validate: calls %d", productCalls)
		}
		if updated.IGDBGameID == nil || *updated.IGDBGameID != 1000 {
			t.Fatalf("identity must snapshot from the validated target: %v", updated.IGDBGameID)
		}
	})

	t.Run("custom display fields replace on custom entries", func(t *testing.T) {
		owner := uuid.New()
		cust := storedGameEntry(owner)
		cust.ProductID = nil
		cust.PricingMode = "disabled"
		var updated store.Entry
		st := &stubStore{
			getEntry: func(context.Context, uuid.UUID, uuid.UUID) (store.Entry, error) { return cust, nil },
			updateEntry: func(_ context.Context, in store.Entry, _ []uuid.UUID) (store.Entry, error) {
				updated = in
				in.Tags = []store.TagRef{}
				return in, nil
			},
		}
		enrich := &stubEnrichment{
			getProduct: func(_ context.Context, _ string, id uuid.UUID) (enrichapi.Product, error) {
				return gameProduct(id), nil
			},
			batchPrices: pricedAs(1500, 4200, 9900),
		}
		srv, a := newUnitServer(t, st, enrich, newStubCache())
		resp := do(t, http.MethodPut, srv.URL+"/entries/"+cust.ID.String(), a.token(t, owner.String()),
			updateBody(func(m map[string]any) {
				m["pricing_mode"] = "disabled"
				m["display_name"] = "Renamed repro"
				m["platform_name"] = "Super Famicom"
			}))
		if resp.StatusCode != http.StatusOK {
			t.Fatalf("custom update: %d", resp.StatusCode)
		}
		if updated.DisplayName != "Renamed repro" || updated.PlatformName == nil ||
			*updated.PlatformName != "Super Famicom" || updated.FirstReleaseDate != nil {
			t.Fatalf("custom fields: %+v", updated)
		}

		// Setting a proxy adopts the target's identity; removing the
		// proxy clears it.
		resp = do(t, http.MethodPut, srv.URL+"/entries/"+cust.ID.String(), a.token(t, owner.String()),
			updateBody(func(m map[string]any) {
				m["pricing_mode"] = "proxy"
				m["pricing_product_id"] = uuid.NewString()
				m["display_name"] = "Renamed repro"
			}))
		if resp.StatusCode != http.StatusOK || updated.IGDBGameID == nil || *updated.IGDBGameID != 1000 {
			t.Fatalf("proxy adoption: %d %v", resp.StatusCode, updated.IGDBGameID)
		}
		cust.PricingMode = "proxy"
		cust.PricingProductID = new(uuid.New())
		cust.IGDBGameID = new(int64(1000))
		resp = do(t, http.MethodPut, srv.URL+"/entries/"+cust.ID.String(), a.token(t, owner.String()),
			updateBody(func(m map[string]any) {
				m["pricing_mode"] = "disabled"
				m["display_name"] = "Renamed repro"
			}))
		if resp.StatusCode != http.StatusOK || updated.IGDBGameID != nil {
			t.Fatalf("identity must clear with the proxy: %d %v", resp.StatusCode, updated.IGDBGameID)
		}

		// Custom PUTs require display_name (full replacement)...
		resp = do(t, http.MethodPut, srv.URL+"/entries/"+cust.ID.String(), a.token(t, owner.String()),
			updateBody(func(m map[string]any) { m["pricing_mode"] = "disabled" }))
		wantProblem(t, resp, http.StatusBadRequest, "invalid_body")
		// ...and can never flip to auto (updateBody's default mode).
		resp = do(t, http.MethodPut, srv.URL+"/entries/"+cust.ID.String(), a.token(t, owner.String()),
			updateBody(func(m map[string]any) { m["display_name"] = "x" }))
		wantProblem(t, resp, http.StatusBadRequest, "invalid_body")
	})

	t.Run("custom platform_igdb_id requires platform_name", func(t *testing.T) {
		// Update is full-replacement: the row already carries a valid
		// pairing, but a body that sets platform_igdb_id while omitting
		// platform_name would clear the name and keep the id - the same
		// invalid pairing the DB's CHECK(platform_igdb_id IS NULL OR
		// platform_name IS NOT NULL) rejects, regardless of current row
		// state. updateEntry stands in for that constraint: if
		// validation ever lets this body through, the store answers the
		// way the real violation would.
		owner := uuid.New()
		cust := storedGameEntry(owner)
		cust.ProductID = nil
		cust.PricingMode = "disabled"
		cust.PlatformName = new("SNES")
		cust.PlatformIGDBID = new(int64(19))
		st := &stubStore{
			getEntry: func(context.Context, uuid.UUID, uuid.UUID) (store.Entry, error) { return cust, nil },
			updateEntry: func(context.Context, store.Entry, []uuid.UUID) (store.Entry, error) {
				return store.Entry{}, errors.New(`pq: check constraint "products_platform_pairing" violated`)
			},
		}
		srv, a := newUnitServer(t, st, &stubEnrichment{}, newStubCache())
		resp := do(t, http.MethodPut, srv.URL+"/entries/"+cust.ID.String(), a.token(t, owner.String()),
			updateBody(func(m map[string]any) {
				m["pricing_mode"] = "disabled"
				m["display_name"] = "Renamed repro"
				m["platform_igdb_id"] = 19
			}))
		if resp.StatusCode != http.StatusBadRequest {
			t.Fatalf("status: got %d, want 400", resp.StatusCode)
		}
		var p struct {
			Code   string `json:"code"`
			Detail string `json:"detail"`
		}
		if err := json.NewDecoder(resp.Body).Decode(&p); err != nil {
			t.Fatal(err)
		}
		if p.Code != "invalid_body" {
			t.Fatalf("code: got %q, want invalid_body", p.Code)
		}
		if p.Detail != "platform_igdb_id requires platform_name" {
			t.Fatalf("detail: got %q", p.Detail)
		}
	})

	t.Run("product-backed entries reject catalog fields", func(t *testing.T) {
		st := &stubStore{getEntry: func(context.Context, uuid.UUID, uuid.UUID) (store.Entry, error) { return e, nil }}
		srv, a := newUnitServer(t, st, &stubEnrichment{}, newStubCache())
		resp := do(t, http.MethodPut, srv.URL+"/entries/"+e.ID.String(), a.token(t, user.String()),
			updateBody(func(m map[string]any) { m["display_name"] = "hijack" }))
		wantProblem(t, resp, http.StatusBadRequest, "invalid_body")
	})

	t.Run("foreign entry answers 404", func(t *testing.T) {
		st := &stubStore{getEntry: func(context.Context, uuid.UUID, uuid.UUID) (store.Entry, error) {
			return store.Entry{}, store.ErrNotFound
		}}
		srv, a := newUnitServer(t, st, &stubEnrichment{}, newStubCache())
		resp := do(t, http.MethodPut, srv.URL+"/entries/"+uuid.NewString(), a.token(t, uuid.NewString()),
			updateBody(nil))
		wantProblem(t, resp, http.StatusNotFound, "entry_not_found")
	})

	t.Run("narrow product re-match", func(t *testing.T) {
		gameProd := func(id uuid.UUID, gameID, platformID int64, matched bool) enrichapi.Product {
			p := enrichapi.Product{Id: id, Type: "game",
				Igdb:     &common.IgdbMeta{GameId: gameID},
				Platform: &common.PlatformRef{IgdbPlatformId: platformID, Name: "SNES"}}
			if matched {
				p.Pricecharting = &common.PricechartingMeta{PcProductId: 5011}
			}
			return p
		}
		target := uuid.New()
		catalog := func(cur enrichapi.Product) *stubEnrichment {
			return &stubEnrichment{
				getProduct: func(_ context.Context, _ string, id uuid.UUID) (enrichapi.Product, error) {
					if id == *e.ProductID {
						return cur, nil
					}
					if id == target {
						return gameProd(target, 1011, 19, true), nil
					}
					return enrichapi.Product{}, enrichmentclient.ErrUnknownProduct
				},
				batchPrices: pricedAs(1500, 4200, 9900),
			}
		}
		repointBody := func(productID string, tweak func(map[string]any)) *bytes.Reader {
			return updateBody(func(m map[string]any) {
				m["product_id"] = productID
				if tweak != nil {
					tweak(m)
				}
			})
		}
		okStore := func(updated *store.Entry) *stubStore {
			return &stubStore{
				getEntry: func(context.Context, uuid.UUID, uuid.UUID) (store.Entry, error) { return e, nil },
				updateEntry: func(_ context.Context, in store.Entry, _ []uuid.UUID) (store.Entry, error) {
					if updated != nil {
						*updated = in
					}
					in.Tags = []store.TagRef{}
					return in, nil
				},
			}
		}

		t.Run("happy path repoints and keeps snapshots", func(t *testing.T) {
			var updated store.Entry
			srv, a := newUnitServer(t, okStore(&updated), catalog(gameProd(*e.ProductID, 1011, 19, false)), newStubCache())
			resp := do(t, http.MethodPut, srv.URL+"/entries/"+e.ID.String(), a.token(t, user.String()),
				repointBody(target.String(), nil))
			if resp.StatusCode != http.StatusOK {
				t.Fatalf("status %d", resp.StatusCode)
			}
			if updated.ProductID == nil || *updated.ProductID != target {
				t.Fatalf("must repoint: %+v", updated.ProductID)
			}
			if updated.DisplayName != e.DisplayName {
				t.Fatal("snapshotted display fields must stay")
			}
		})
		t.Run("repoint re-picks the date from the new product's release dates", func(t *testing.T) {
			// A repoint always re-fetches the new product (needed for
			// the family check); the date pick reuses that same fetch
			// rather than triggering a second GetProduct call.
			releaseDates := []common.ReleaseDate{
				{Region: "japan", Date: openapi_types.Date{Time: time.Date(1995, time.March, 11, 0, 0, 0, 0, time.UTC)}},
				{Region: "north_america", Date: openapi_types.Date{Time: time.Date(1995, time.August, 22, 0, 0, 0, 0, time.UTC)}},
			}
			var productCalls int
			enrich := &stubEnrichment{
				getProduct: func(_ context.Context, _ string, id uuid.UUID) (enrichapi.Product, error) {
					productCalls++
					if id == *e.ProductID {
						return gameProd(*e.ProductID, 1011, 19, false), nil
					}
					if id == target {
						p := gameProd(target, 1011, 19, true)
						p.Igdb.ReleaseDates = &releaseDates
						return p, nil
					}
					return enrichapi.Product{}, enrichmentclient.ErrUnknownProduct
				},
				batchPrices: pricedAs(1500, 4200, 9900),
			}
			var updated store.Entry
			srv, a := newUnitServer(t, okStore(&updated), enrich, newStubCache())
			resp := do(t, http.MethodPut, srv.URL+"/entries/"+e.ID.String(), a.token(t, user.String()),
				repointBody(target.String(), nil)) // region stays ntsc_u
			if resp.StatusCode != http.StatusOK {
				t.Fatalf("status %d", resp.StatusCode)
			}
			want := time.Date(1995, time.August, 22, 0, 0, 0, 0, time.UTC) // north_america: the ntsc_u chain's first hit
			if updated.FirstReleaseDate == nil || !updated.FirstReleaseDate.Equal(want) {
				t.Fatalf("repoint must re-pick from the new product: %v", updated.FirstReleaseDate)
			}
			if productCalls != 2 {
				t.Fatalf("GetProduct calls: got %d, want 2 (current product + new product)", productCalls)
			}
		})
		t.Run("repoint re-picks the localized trio from the new product", func(t *testing.T) {
			// Region unchanged (pal on both sides), so the repoint is the
			// only trigger - the region-edit trigger has its own coverage
			// in TestUnitUpdateEntry_RegionEditRePicksLocalization.
			pal := e
			pal.Region = "pal"
			var updated store.Entry
			st := &stubStore{
				getEntry: func(context.Context, uuid.UUID, uuid.UUID) (store.Entry, error) { return pal, nil },
				updateEntry: func(_ context.Context, in store.Entry, _ []uuid.UUID) (store.Entry, error) {
					updated = in
					in.Tags = []store.TagRef{}
					return in, nil
				},
			}
			enrich := &stubEnrichment{
				getProduct: func(_ context.Context, _ string, id uuid.UUID) (enrichapi.Product, error) {
					if id == *e.ProductID {
						return gameProd(*e.ProductID, 1011, 19, false), nil
					}
					if id == target {
						p := gameProd(target, 1011, 19, true)
						p.Igdb.Localizations = &[]common.Localization{
							{Region: "EU", CoverUrl: new("https://images.igdb.example/eu.jpg")},
						}
						return p, nil
					}
					return enrichapi.Product{}, enrichmentclient.ErrUnknownProduct
				},
				batchPrices: pricedAs(1500, 4200, 9900),
			}
			srv, a := newUnitServer(t, st, enrich, newStubCache())
			resp := do(t, http.MethodPut, srv.URL+"/entries/"+e.ID.String(), a.token(t, user.String()),
				repointBody(target.String(), func(m map[string]any) { m["region"] = "pal" }))
			if resp.StatusCode != http.StatusOK {
				t.Fatalf("status %d", resp.StatusCode)
			}
			if updated.LocalizedCoverURL == nil || *updated.LocalizedCoverURL != "https://images.igdb.example/eu.jpg" ||
				updated.LocalizedName != nil || updated.LocalizedNameTranslit != nil {
				t.Fatalf("repoint must re-pick the EU bundle: %v %v %v",
					updated.LocalizedName, updated.LocalizedNameTranslit, updated.LocalizedCoverURL)
			}
		})
		t.Run("same id is a no-op, no catalog calls", func(t *testing.T) {
			srv, a := newUnitServer(t, okStore(nil), &stubEnrichment{batchPrices: pricedAs(1, 2, 3)}, newStubCache())
			resp := do(t, http.MethodPut, srv.URL+"/entries/"+e.ID.String(), a.token(t, user.String()),
				repointBody(e.ProductID.String(), nil))
			if resp.StatusCode != http.StatusOK {
				t.Fatalf("status %d", resp.StatusCode)
			}
		})
		t.Run("non-auto pricing mode is refused", func(t *testing.T) {
			srv, a := newUnitServer(t, okStore(nil), catalog(gameProd(*e.ProductID, 1011, 19, false)), newStubCache())
			resp := do(t, http.MethodPut, srv.URL+"/entries/"+e.ID.String(), a.token(t, user.String()),
				repointBody(target.String(), func(m map[string]any) { m["pricing_mode"] = "disabled" }))
			wantProblem(t, resp, http.StatusBadRequest, "invalid_product_change")
		})
		t.Run("matched current product is refused", func(t *testing.T) {
			srv, a := newUnitServer(t, okStore(nil), catalog(gameProd(*e.ProductID, 1011, 19, true)), newStubCache())
			resp := do(t, http.MethodPut, srv.URL+"/entries/"+e.ID.String(), a.token(t, user.String()),
				repointBody(target.String(), nil))
			wantProblem(t, resp, http.StatusBadRequest, "invalid_product_change")
		})
		t.Run("cross-family target is refused", func(t *testing.T) {
			enrich := catalog(gameProd(*e.ProductID, 1005, 4, false)) // different family than the target
			srv, a := newUnitServer(t, okStore(nil), enrich, newStubCache())
			resp := do(t, http.MethodPut, srv.URL+"/entries/"+e.ID.String(), a.token(t, user.String()),
				repointBody(target.String(), nil))
			wantProblem(t, resp, http.StatusBadRequest, "invalid_product_change")
		})
		t.Run("unknown target is refused", func(t *testing.T) {
			srv, a := newUnitServer(t, okStore(nil), catalog(gameProd(*e.ProductID, 1011, 19, false)), newStubCache())
			resp := do(t, http.MethodPut, srv.URL+"/entries/"+e.ID.String(), a.token(t, user.String()),
				repointBody(uuid.NewString(), nil))
			wantProblem(t, resp, http.StatusBadRequest, "invalid_product_change")
		})
		t.Run("enrichment down answers 502, entry unchanged", func(t *testing.T) {
			enrich := &stubEnrichment{getProduct: func(context.Context, string, uuid.UUID) (enrichapi.Product, error) {
				return enrichapi.Product{}, enrichmentclient.ErrUnavailable
			}}
			srv, a := newUnitServer(t, okStore(nil), enrich, newStubCache())
			resp := do(t, http.MethodPut, srv.URL+"/entries/"+e.ID.String(), a.token(t, user.String()),
				repointBody(target.String(), nil))
			wantProblem(t, resp, http.StatusBadGateway, "enrichment_unavailable")
		})
		t.Run("custom entries are refused", func(t *testing.T) {
			ce := storedGameEntry(user)
			ce.ProductID = nil
			ce.PricingMode = "disabled"
			st := &stubStore{getEntry: func(context.Context, uuid.UUID, uuid.UUID) (store.Entry, error) { return ce, nil }}
			srv, a := newUnitServer(t, st, &stubEnrichment{}, newStubCache())
			resp := do(t, http.MethodPut, srv.URL+"/entries/"+ce.ID.String(), a.token(t, user.String()),
				updateBody(func(m map[string]any) {
					m["product_id"] = uuid.NewString()
					m["display_name"] = ce.DisplayName
					m["pricing_mode"] = "disabled"
				}))
			wantProblem(t, resp, http.StatusBadRequest, "invalid_product_change")
		})
	})
}

// The narrow product_id re-match arm stamps user server-side.
func TestUpdateEntry_NarrowRematchStampsUser(t *testing.T) {
	user := uuid.New()
	e := storedGameEntry(user)
	target := uuid.New()
	curProd := enrichapi.Product{Id: *e.ProductID, Type: "game",
		Igdb:     &common.IgdbMeta{GameId: 1011},
		Platform: &common.PlatformRef{IgdbPlatformId: 19, Name: "SNES"}} // unmatched: required for re-match eligibility
	newProd := enrichapi.Product{Id: target, Type: "game",
		Igdb:          &common.IgdbMeta{GameId: 1011},
		Platform:      &common.PlatformRef{IgdbPlatformId: 19, Name: "SNES"},
		Pricecharting: &common.PricechartingMeta{PcProductId: 5011}}
	var updated store.Entry
	st := &stubStore{
		getEntry: func(context.Context, uuid.UUID, uuid.UUID) (store.Entry, error) { return e, nil },
		updateEntry: func(_ context.Context, in store.Entry, _ []uuid.UUID) (store.Entry, error) {
			updated = in
			in.Tags = []store.TagRef{}
			return in, nil
		},
	}
	enrich := &stubEnrichment{
		getProduct: func(_ context.Context, _ string, id uuid.UUID) (enrichapi.Product, error) {
			if id == *e.ProductID {
				return curProd, nil
			}
			if id == target {
				return newProd, nil
			}
			return enrichapi.Product{}, enrichmentclient.ErrUnknownProduct
		},
		batchPrices: pricedAs(1500, 4200, 9900),
	}
	srv, a := newUnitServer(t, st, enrich, newStubCache())
	resp := do(t, http.MethodPut, srv.URL+"/entries/"+e.ID.String(), a.token(t, user.String()),
		updateBody(func(m map[string]any) { m["product_id"] = target.String() }))
	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		t.Fatalf("status %d: %s", resp.StatusCode, body)
	}
	if updated.ProductID == nil || *updated.ProductID != target {
		t.Fatalf("must repoint: %v", updated.ProductID)
	}
	if updated.MatchProvenance != "user" {
		t.Fatalf("the narrow re-match must stamp match_provenance user, got %q", updated.MatchProvenance)
	}
}

// A plain edit (notes/tags/status) on a user-provenance entry leaves
// the column user.
func TestUpdateEntry_PlainEditPreservesProvenance(t *testing.T) {
	user := uuid.New()
	e := storedGameEntry(user)
	e.MatchProvenance = "user"
	var updated store.Entry
	st := &stubStore{
		getEntry: func(context.Context, uuid.UUID, uuid.UUID) (store.Entry, error) { return e, nil },
		updateEntry: func(_ context.Context, in store.Entry, _ []uuid.UUID) (store.Entry, error) {
			updated = in
			in.Tags = []store.TagRef{}
			return in, nil
		},
	}
	enrich := &stubEnrichment{batchPrices: pricedAs(1500, 4200, 9900)}
	srv, a := newUnitServer(t, st, enrich, newStubCache())
	resp := do(t, http.MethodPut, srv.URL+"/entries/"+e.ID.String(), a.token(t, user.String()),
		updateBody(func(m map[string]any) {
			m["notes"] = "great cart"
			m["status"] = "playing"
		}))
	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		t.Fatalf("status %d: %s", resp.StatusCode, body)
	}
	if updated.MatchProvenance != "user" {
		t.Fatalf("a plain edit must preserve match_provenance: got %q", updated.MatchProvenance)
	}
}

// TestUnitUpdateEntry_RegionScopedReleaseDate covers the snapshot
// re-pick triggers introduced for region-scoped dates: a region edit
// on a game-backed entry re-fetches its product and re-picks, an
// unchanged region never fetches, a fetch failure on a region-only
// edit is a hard 502 (products are never deleted, so any failure here
// reads as an availability problem), and hardware (no igdb_game_id)
// never fetches on a region edit even though it is product-backed.
func TestUnitUpdateEntry_RegionScopedReleaseDate(t *testing.T) {
	user := uuid.New()
	naDate := time.Date(1995, time.August, 22, 0, 0, 0, 0, time.UTC)
	euDate := time.Date(1995, time.November, 24, 0, 0, 0, 0, time.UTC)
	dated := func(id uuid.UUID) enrichapi.Product {
		p := gameProduct(id)
		p.Igdb.ReleaseDates = &[]common.ReleaseDate{
			{Region: "north_america", Date: openapi_types.Date{Time: naDate}},
			{Region: "europe", Date: openapi_types.Date{Time: euDate}},
		}
		return p
	}
	// gameBacked carries an igdb game id and an already-picked date -
	// the precondition for a region edit to be fetch-eligible at all.
	gameBacked := func() store.Entry {
		e := storedGameEntry(user)
		e.IGDBGameID = new(int64(1000))
		d := naDate
		e.FirstReleaseDate = &d
		return e
	}

	t.Run("region-only change fetches exactly once and re-picks", func(t *testing.T) {
		e := gameBacked()
		var calls int
		var updated store.Entry
		st := &stubStore{
			getEntry: func(context.Context, uuid.UUID, uuid.UUID) (store.Entry, error) { return e, nil },
			updateEntry: func(_ context.Context, in store.Entry, _ []uuid.UUID) (store.Entry, error) {
				updated = in
				in.Tags = []store.TagRef{}
				return in, nil
			},
		}
		enrich := &stubEnrichment{
			getProduct: func(_ context.Context, _ string, id uuid.UUID) (enrichapi.Product, error) {
				calls++
				return dated(id), nil
			},
			batchPrices: pricedAs(1500, 4200, 9900),
		}
		srv, a := newUnitServer(t, st, enrich, newStubCache())
		resp := do(t, http.MethodPut, srv.URL+"/entries/"+e.ID.String(), a.token(t, user.String()),
			updateBody(func(m map[string]any) { m["region"] = "pal" }))
		if resp.StatusCode != http.StatusOK {
			body, _ := io.ReadAll(resp.Body)
			t.Fatalf("status %d: %s", resp.StatusCode, body)
		}
		if calls != 1 {
			t.Fatalf("GetProduct calls: %d", calls)
		}
		if updated.FirstReleaseDate == nil || !updated.FirstReleaseDate.Equal(euDate) {
			t.Fatalf("re-picked date: %v", updated.FirstReleaseDate)
		}
	})

	t.Run("region unchanged makes no fetch, date carries through", func(t *testing.T) {
		e := gameBacked()
		var calls int
		var updated store.Entry
		st := &stubStore{
			getEntry: func(context.Context, uuid.UUID, uuid.UUID) (store.Entry, error) { return e, nil },
			updateEntry: func(_ context.Context, in store.Entry, _ []uuid.UUID) (store.Entry, error) {
				updated = in
				in.Tags = []store.TagRef{}
				return in, nil
			},
		}
		enrich := &stubEnrichment{
			getProduct: func(_ context.Context, _ string, id uuid.UUID) (enrichapi.Product, error) {
				calls++
				return dated(id), nil
			},
			batchPrices: pricedAs(1500, 4200, 9900),
		}
		srv, a := newUnitServer(t, st, enrich, newStubCache())
		resp := do(t, http.MethodPut, srv.URL+"/entries/"+e.ID.String(), a.token(t, user.String()),
			updateBody(nil)) // region defaults to ntsc_u, same as stored
		if resp.StatusCode != http.StatusOK {
			t.Fatalf("status %d", resp.StatusCode)
		}
		if calls != 0 {
			t.Fatalf("GetProduct calls: %d", calls)
		}
		if updated.FirstReleaseDate == nil || !updated.FirstReleaseDate.Equal(naDate) {
			t.Fatalf("date must carry through unchanged: %v", updated.FirstReleaseDate)
		}
	})

	t.Run("region change with enrichment down is 502, entry unchanged", func(t *testing.T) {
		e := gameBacked()
		st := &stubStore{
			getEntry: func(context.Context, uuid.UUID, uuid.UUID) (store.Entry, error) { return e, nil },
			// updateEntry deliberately nil: a fetch failure on the
			// region-only trigger must return before any store write.
		}
		enrich := &stubEnrichment{getProduct: func(context.Context, string, uuid.UUID) (enrichapi.Product, error) {
			return enrichapi.Product{}, enrichmentclient.ErrUnavailable
		}}
		srv, a := newUnitServer(t, st, enrich, newStubCache())
		resp := do(t, http.MethodPut, srv.URL+"/entries/"+e.ID.String(), a.token(t, user.String()),
			updateBody(func(m map[string]any) { m["region"] = "pal" }))
		wantProblem(t, resp, http.StatusBadGateway, "enrichment_unavailable")
	})

	t.Run("hardware region edit never fetches", func(t *testing.T) {
		hw := storedGameEntry(user)
		hw.ItemType = "console"
		hw.DisplayName = "Super NES Console"
		// IGDBGameID stays nil: hardware has no igdb identity, so a
		// region edit must not depend on enrichment being up.
		var calls int
		var updated store.Entry
		st := &stubStore{
			getEntry: func(context.Context, uuid.UUID, uuid.UUID) (store.Entry, error) { return hw, nil },
			updateEntry: func(_ context.Context, in store.Entry, _ []uuid.UUID) (store.Entry, error) {
				updated = in
				in.Tags = []store.TagRef{}
				return in, nil
			},
		}
		enrich := &stubEnrichment{
			getProduct: func(_ context.Context, _ string, id uuid.UUID) (enrichapi.Product, error) {
				calls++
				return dated(id), nil
			},
			batchPrices: pricedAs(1500, 4200, 9900),
		}
		srv, a := newUnitServer(t, st, enrich, newStubCache())
		resp := do(t, http.MethodPut, srv.URL+"/entries/"+hw.ID.String(), a.token(t, user.String()),
			updateBody(func(m map[string]any) { m["region"] = "pal" }))
		if resp.StatusCode != http.StatusOK {
			t.Fatalf("status %d", resp.StatusCode)
		}
		if calls != 0 {
			t.Fatalf("GetProduct calls: %d", calls)
		}
		if updated.FirstReleaseDate != nil {
			t.Fatalf("hardware date must stay untouched: %v", updated.FirstReleaseDate)
		}
		if updated.LocalizedName != nil || updated.LocalizedNameTranslit != nil || updated.LocalizedCoverURL != nil {
			t.Fatalf("hardware localized fields must stay untouched: %v %v %v",
				updated.LocalizedName, updated.LocalizedNameTranslit, updated.LocalizedCoverURL)
		}
	})
}

// TestUnitUpdateEntry_RegionEditRePicksLocalization is the PUT-side
// half of the localized snapshot: the region edit that re-picks the
// date re-picks the presentation trio from the same fetch, and moving
// into a region with no localized form clears what the old region
// stored instead of leaving a stale native-script title behind.
func TestUnitUpdateEntry_RegionEditRePicksLocalization(t *testing.T) {
	user := uuid.New()
	gameBacked := func(region string) store.Entry {
		e := storedGameEntry(user)
		e.Region = region
		e.IGDBGameID = new(int64(1000))
		return e
	}
	newEnrich := func(calls *int) *stubEnrichment {
		return &stubEnrichment{
			getProduct: func(_ context.Context, _ string, id uuid.UUID) (enrichapi.Product, error) {
				*calls++
				return localizedGameProduct(id), nil
			},
			batchPrices: pricedAs(1500, 4200, 9900),
		}
	}
	newStore := func(e store.Entry, updated *store.Entry) *stubStore {
		return &stubStore{
			getEntry: func(context.Context, uuid.UUID, uuid.UUID) (store.Entry, error) { return e, nil },
			updateEntry: func(_ context.Context, in store.Entry, _ []uuid.UUID) (store.Entry, error) {
				*updated = in
				in.Tags = []store.TagRef{}
				return in, nil
			},
		}
	}

	t.Run("ntsc_u to ntsc_j picks the ja-JP bundle", func(t *testing.T) {
		e := gameBacked("ntsc_u")
		var calls int
		var updated store.Entry
		srv, a := newUnitServer(t, newStore(e, &updated), newEnrich(&calls), newStubCache())
		resp := do(t, http.MethodPut, srv.URL+"/entries/"+e.ID.String(), a.token(t, user.String()),
			updateBody(func(m map[string]any) { m["region"] = "ntsc_j" }))
		if resp.StatusCode != http.StatusOK {
			body, _ := io.ReadAll(resp.Body)
			t.Fatalf("status %d: %s", resp.StatusCode, body)
		}
		if calls != 1 {
			t.Fatalf("GetProduct calls: %d", calls)
		}
		if updated.LocalizedName == nil || *updated.LocalizedName != "聖剣伝説3" ||
			updated.LocalizedNameTranslit == nil || *updated.LocalizedNameTranslit != "Seiken Densetsu 3" ||
			updated.LocalizedCoverURL == nil || *updated.LocalizedCoverURL != "https://images.igdb.example/jp.jpg" {
			t.Fatalf("re-picked localized snapshot: %v %v %v",
				updated.LocalizedName, updated.LocalizedNameTranslit, updated.LocalizedCoverURL)
		}
		var got struct {
			LocalizedName         *string `json:"localized_name"`
			LocalizedNameTranslit *string `json:"localized_name_translit"`
			LocalizedCoverURL     *string `json:"localized_cover_url"`
		}
		if err := json.NewDecoder(resp.Body).Decode(&got); err != nil {
			t.Fatal(err)
		}
		if got.LocalizedName == nil || *got.LocalizedName != "聖剣伝説3" ||
			got.LocalizedNameTranslit == nil || *got.LocalizedNameTranslit != "Seiken Densetsu 3" ||
			got.LocalizedCoverURL == nil || *got.LocalizedCoverURL != "https://images.igdb.example/jp.jpg" {
			t.Fatalf("response localized snapshot: %v %v %v",
				got.LocalizedName, got.LocalizedNameTranslit, got.LocalizedCoverURL)
		}
	})

	t.Run("ntsc_j to ntsc_u clears the previous region's pick", func(t *testing.T) {
		e := gameBacked("ntsc_j")
		e.LocalizedName = new("聖剣伝説3")
		e.LocalizedNameTranslit = new("Seiken Densetsu 3")
		e.LocalizedCoverURL = new("https://images.igdb.example/jp.jpg")
		var calls int
		var updated store.Entry
		srv, a := newUnitServer(t, newStore(e, &updated), newEnrich(&calls), newStubCache())
		resp := do(t, http.MethodPut, srv.URL+"/entries/"+e.ID.String(), a.token(t, user.String()),
			updateBody(nil)) // region defaults to ntsc_u, which chains nowhere
		if resp.StatusCode != http.StatusOK {
			t.Fatalf("status %d", resp.StatusCode)
		}
		if calls != 1 {
			t.Fatalf("GetProduct calls: %d", calls)
		}
		if updated.LocalizedName != nil || updated.LocalizedNameTranslit != nil || updated.LocalizedCoverURL != nil {
			t.Fatalf("stale localized snapshot survived the region edit: %v %v %v",
				updated.LocalizedName, updated.LocalizedNameTranslit, updated.LocalizedCoverURL)
		}
	})
}

func TestUnitDeleteEntry(t *testing.T) {
	user := uuid.New()
	id := uuid.New()
	deleted := false
	st := &stubStore{deleteEntry: func(_ context.Context, gotUser, gotID uuid.UUID) error {
		if gotUser != user || gotID != id {
			return store.ErrNotFound
		}
		deleted = true
		return nil
	}}
	c := newStubCache()
	srv, a := newUnitServer(t, st, &stubEnrichment{}, c)

	resp := do(t, http.MethodDelete, srv.URL+"/entries/"+id.String(), a.token(t, user.String()), nil)
	if resp.StatusCode != http.StatusNoContent || !deleted {
		t.Fatalf("delete: %d %v", resp.StatusCode, deleted)
	}
	if len(c.invalidated) != 1 {
		t.Fatalf("invalidations: %v", c.invalidated)
	}
	resp = do(t, http.MethodDelete, srv.URL+"/entries/"+uuid.NewString(), a.token(t, user.String()), nil)
	wantProblem(t, resp, http.StatusNotFound, "entry_not_found")
}

// TestAckEntryRegionMismatch pins the handler's ownership idiom: 204
// and a stamp call for the owner, 404 entry_not_found for someone
// else's entry (the store's ownership WHERE reports ErrNotFound
// identically for foreign and missing rows, same as DeleteEntry).
func TestAckEntryRegionMismatch(t *testing.T) {
	user := uuid.New()
	id := uuid.New()
	stamped := 0
	st := &stubStore{ackRegionMismatch: func(_ context.Context, gotUser, gotID uuid.UUID) error {
		if gotUser != user || gotID != id {
			return store.ErrNotFound
		}
		stamped++
		return nil
	}}
	srv, a := newUnitServer(t, st, &stubEnrichment{}, newStubCache())

	resp := do(t, http.MethodPost, srv.URL+"/entries/"+id.String()+"/region-mismatch-ack", a.token(t, user.String()), nil)
	if resp.StatusCode != http.StatusNoContent || stamped != 1 {
		t.Fatalf("ack: %d, stamp calls %d", resp.StatusCode, stamped)
	}

	resp = do(t, http.MethodPost, srv.URL+"/entries/"+id.String()+"/region-mismatch-ack", a.token(t, uuid.NewString()), nil)
	wantProblem(t, resp, http.StatusNotFound, "entry_not_found")
}

func TestUpdateEntryRankTransitionThroughTheStack(t *testing.T) {
	s := newStack(t)
	productID := s.enrich.addGame("Chrono Trigger", 1500, 4200, 9900)
	sub := uuid.New()
	tok := s.auth.token(t, sub.String())

	resp := do(t, http.MethodPost, s.baseURL+"/entries", tok, createBody(productID, nil))
	var created struct {
		ID          uuid.UUID `json:"id"`
		BacklogRank *string   `json:"backlog_rank"`
	}
	_ = json.NewDecoder(resp.Body).Decode(&created)
	if created.BacklogRank == nil {
		t.Fatal("backlog create must carry a rank")
	}

	// Leave the backlog: the rank clears (the DB CHECK would reject
	// anything else).
	resp = do(t, http.MethodPut, s.baseURL+"/entries/"+created.ID.String(), tok, updateBody(nil))
	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		t.Fatalf("update: %d %s", resp.StatusCode, body)
	}
	var afterLeave struct {
		Status      string  `json:"status"`
		BacklogRank *string `json:"backlog_rank"`
	}
	_ = json.NewDecoder(resp.Body).Decode(&afterLeave)
	if afterLeave.Status != "beaten" || afterLeave.BacklogRank != nil {
		t.Fatalf("after leave: %+v", afterLeave)
	}

	// Delete through HTTP; the row is gone.
	resp = do(t, http.MethodDelete, s.baseURL+"/entries/"+created.ID.String(), tok, nil)
	if resp.StatusCode != http.StatusNoContent {
		t.Fatalf("delete: %d", resp.StatusCode)
	}
	if _, err := s.store.GetEntry(context.Background(), sub, created.ID); !errors.Is(err, store.ErrNotFound) {
		t.Fatal("row must be gone")
	}
}

func reorderBody(after, before *uuid.UUID) *bytes.Reader {
	m := map[string]any{}
	if after != nil {
		m["after_id"] = after.String()
	}
	if before != nil {
		m["before_id"] = before.String()
	}
	b, _ := json.Marshal(m)
	return bytes.NewReader(b)
}

func TestUnitReorderEntry(t *testing.T) {
	user := uuid.New()
	e := storedGameEntry(user)
	after, before := uuid.New(), uuid.New()

	t.Run("happy move passes neighbors through", func(t *testing.T) {
		var gotAfter, gotBefore *uuid.UUID
		st := &stubStore{reorder: func(_ context.Context, _, _ uuid.UUID, a, b *uuid.UUID) (store.Entry, error) {
			gotAfter, gotBefore = a, b
			return e, nil
		}}
		enrich := &stubEnrichment{batchPrices: pricedAs(1500, 4200, 9900)}
		c := newStubCache()
		srv, a := newUnitServer(t, st, enrich, c)
		resp := do(t, http.MethodPost, srv.URL+"/entries/"+e.ID.String()+"/reorder",
			a.token(t, user.String()), reorderBody(&after, &before))
		if resp.StatusCode != http.StatusOK {
			t.Fatalf("status %d", resp.StatusCode)
		}
		if gotAfter == nil || *gotAfter != after || gotBefore == nil || *gotBefore != before {
			t.Fatalf("neighbors: %v %v", gotAfter, gotBefore)
		}
		if len(c.invalidated) != 1 {
			t.Fatalf("invalidations: %v", c.invalidated)
		}
	})

	t.Run("neighborless request is 400", func(t *testing.T) {
		srv, a := newUnitServer(t, &stubStore{}, &stubEnrichment{}, newStubCache())
		resp := do(t, http.MethodPost, srv.URL+"/entries/"+e.ID.String()+"/reorder",
			a.token(t, user.String()), reorderBody(nil, nil))
		wantProblem(t, resp, http.StatusBadRequest, "invalid_body")
	})

	t.Run("self neighbor is 400", func(t *testing.T) {
		srv, a := newUnitServer(t, &stubStore{}, &stubEnrichment{}, newStubCache())
		self := e.ID
		resp := do(t, http.MethodPost, srv.URL+"/entries/"+e.ID.String()+"/reorder",
			a.token(t, user.String()), reorderBody(&self, nil))
		wantProblem(t, resp, http.StatusBadRequest, "invalid_body")
	})

	sentinelCases := []struct {
		name string
		err  error
		code string
		want int
	}{
		{"unknown entry", store.ErrNotFound, "entry_not_found", http.StatusNotFound},
		{"not in backlog", store.ErrNotInBacklog, "not_in_backlog", http.StatusConflict},
		{"stale drag", store.ErrConflictingOrder, "conflicting_order", http.StatusConflict},
	}
	for _, tc := range sentinelCases {
		t.Run(tc.name, func(t *testing.T) {
			st := &stubStore{reorder: func(context.Context, uuid.UUID, uuid.UUID, *uuid.UUID, *uuid.UUID) (store.Entry, error) {
				return store.Entry{}, tc.err
			}}
			srv, a := newUnitServer(t, st, &stubEnrichment{}, newStubCache())
			resp := do(t, http.MethodPost, srv.URL+"/entries/"+e.ID.String()+"/reorder",
				a.token(t, user.String()), reorderBody(&after, nil))
			wantProblem(t, resp, tc.want, tc.code)
		})
	}
}

func TestReorderThroughTheStack(t *testing.T) {
	s := newStack(t)
	productID := s.enrich.addGame("Chrono Trigger", 1500, 4200, 9900)
	sub := uuid.New()
	tok := s.auth.token(t, sub.String())

	mkEntry := func() uuid.UUID {
		t.Helper()
		resp := do(t, http.MethodPost, s.baseURL+"/entries", tok, createBody(productID, nil))
		if resp.StatusCode != http.StatusCreated {
			t.Fatalf("create: %d", resp.StatusCode)
		}
		var got struct {
			ID uuid.UUID `json:"id"`
		}
		_ = json.NewDecoder(resp.Body).Decode(&got)
		return got.ID
	}
	a, b, c := mkEntry(), mkEntry(), mkEntry()

	// Drag c between a and b.
	resp := do(t, http.MethodPost, s.baseURL+"/entries/"+c.String()+"/reorder", tok, reorderBody(&a, &b))
	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		t.Fatalf("reorder: %d %s", resp.StatusCode, body)
	}
	ctx := context.Background()
	ea, _ := s.store.GetEntry(ctx, sub, a)
	eb, _ := s.store.GetEntry(ctx, sub, b)
	ec, _ := s.store.GetEntry(ctx, sub, c)
	if *ea.BacklogRank >= *ec.BacklogRank || *ec.BacklogRank >= *eb.BacklogRank {
		t.Fatalf("order: %q %q %q", *ea.BacklogRank, *ec.BacklogRank, *eb.BacklogRank)
	}

	// A stale drag (reversed neighbors) answers 409 conflicting_order.
	resp = do(t, http.MethodPost, s.baseURL+"/entries/"+a.String()+"/reorder", tok, reorderBody(&eb.ID, &ec.ID))
	wantProblem(t, resp, http.StatusConflict, "conflicting_order")
}

// manyUUIDStrings builds n distinct uuid strings for maxItems guard
// tests (the contract's maxItems bounds on entry_ids, add_tag_ids, and
// remove_tag_ids have no existing generator to reuse).
func manyUUIDStrings(n int) []string {
	out := make([]string, n)
	for i := range out {
		out[i] = uuid.NewString()
	}
	return out
}

// TestUnitBulkUpdateEntries_ValidationMatrix mirrors
// TestUnitCreateEntry_ValidationMatrix's idiom: every case reaches an
// empty stubStore (BulkUpdateEntries unset), proving the guard 400s
// before the store is ever touched (a call would panic).
func TestUnitBulkUpdateEntries_ValidationMatrix(t *testing.T) {
	validID := uuid.NewString()
	cases := []struct {
		name string
		body map[string]any
	}{
		{"missing entry_ids", map[string]any{"status": "playing"}},
		{"empty entry_ids", map[string]any{"entry_ids": []string{}, "status": "playing"}},
		{"too many entry_ids", map[string]any{"entry_ids": manyUUIDStrings(201), "status": "playing"}},
		{"too many add_tag_ids", map[string]any{"entry_ids": []string{validID}, "add_tag_ids": manyUUIDStrings(51)}},
		{"too many remove_tag_ids", map[string]any{"entry_ids": []string{validID}, "remove_tag_ids": manyUUIDStrings(51)}},
		{"bad status", map[string]any{"entry_ids": []string{validID}, "status": "queued"}},
		{"storage_location too long", map[string]any{"entry_ids": []string{validID}, "storage_location": strings.Repeat("x", 201)}},
		{"no action present", map[string]any{"entry_ids": []string{validID}}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			srv, a := newUnitServer(t, &stubStore{}, &stubEnrichment{}, newStubCache())
			resp := do(t, http.MethodPost, srv.URL+"/entries/bulk-update", a.token(t, uuid.NewString()), jsonBody(tc.body))
			wantProblem(t, resp, http.StatusBadRequest, "invalid_body")
		})
	}
	// Malformed JSON.
	srv, a := newUnitServer(t, &stubStore{}, &stubEnrichment{}, newStubCache())
	resp := do(t, http.MethodPost, srv.URL+"/entries/bulk-update", a.token(t, uuid.NewString()), bytes.NewReader([]byte("{")))
	wantProblem(t, resp, http.StatusBadRequest, "invalid_body")
}

// TestUnitBulkUpdateEntries_Success proves a valid request reaches
// the store with entry_ids and every action forwarded, and the
// store's count becomes the response's updated_count.
func TestUnitBulkUpdateEntries_Success(t *testing.T) {
	user := uuid.New()
	entryA, entryB, tagID := uuid.New(), uuid.New(), uuid.New()
	var gotUserID uuid.UUID
	var gotEntryIDs []uuid.UUID
	var gotActions store.BulkActions
	st := &stubStore{bulkUpdateEntries: func(_ context.Context, userID uuid.UUID, entryIDs []uuid.UUID, actions store.BulkActions) (int, error) {
		gotUserID, gotEntryIDs, gotActions = userID, entryIDs, actions
		return 2, nil
	}}
	srv, a := newUnitServer(t, st, &stubEnrichment{}, newStubCache())
	resp := do(t, http.MethodPost, srv.URL+"/entries/bulk-update", a.token(t, user.String()),
		jsonBody(map[string]any{
			"entry_ids":        []string{entryA.String(), entryB.String()},
			"add_tag_ids":      []string{tagID.String()},
			"status":           "shelved",
			"storage_location": "closet B",
		}))
	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		t.Fatalf("status %d: %s", resp.StatusCode, body)
	}
	var got struct {
		UpdatedCount int `json:"updated_count"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&got); err != nil {
		t.Fatal(err)
	}
	if got.UpdatedCount != 2 {
		t.Fatalf("updated_count: got %+v, want 2", got)
	}
	if gotUserID != user || len(gotEntryIDs) != 2 || gotEntryIDs[0] != entryA || gotEntryIDs[1] != entryB {
		t.Fatalf("store call: user=%s entryIDs=%v", gotUserID, gotEntryIDs)
	}
	if len(gotActions.AddTagIDs) != 1 || gotActions.AddTagIDs[0] != tagID {
		t.Fatalf("add_tag_ids not forwarded: %+v", gotActions)
	}
	if gotActions.Status == nil || *gotActions.Status != "shelved" {
		t.Fatalf("status not forwarded: %+v", gotActions)
	}
	if gotActions.StorageLocation == nil || *gotActions.StorageLocation != "closet B" {
		t.Fatalf("storage_location not forwarded: %+v", gotActions)
	}
}

// TestUnitBulkUpdateEntries_TagCapExceededMapsTo400 pins the delegated
// status/code choice for the bulk per-entry tag cap: 400, code
// tag_cap_exceeded (distinct from the generic invalid_body every other
// bulk-update guard answers).
func TestUnitBulkUpdateEntries_TagCapExceededMapsTo400(t *testing.T) {
	st := &stubStore{bulkUpdateEntries: func(context.Context, uuid.UUID, []uuid.UUID, store.BulkActions) (int, error) {
		return 0, store.ErrTagCapExceeded
	}}
	srv, a := newUnitServer(t, st, &stubEnrichment{}, newStubCache())
	resp := do(t, http.MethodPost, srv.URL+"/entries/bulk-update", a.token(t, uuid.NewString()),
		jsonBody(map[string]any{"entry_ids": []string{uuid.NewString()}, "add_tag_ids": []string{uuid.NewString()}}))
	wantProblem(t, resp, http.StatusBadRequest, "tag_cap_exceeded")
}

// ---- custom pricing mode ----

func TestUnitCustomPricing_ValidationMatrix(t *testing.T) {
	productID := uuid.New()
	cases := []struct {
		name   string
		mutate func(map[string]any)
		detail string
	}{
		{"custom mode without a value", func(m map[string]any) {
			m["pricing_mode"] = "custom"
		}, "pricing_mode custom requires custom_value_cents"},
		{"negative custom value", func(m map[string]any) {
			m["pricing_mode"] = "custom"
			m["custom_value_cents"] = -5
		}, "custom_value_cents is invalid"},
		{"custom value over cap", func(m map[string]any) {
			m["pricing_mode"] = "custom"
			m["custom_value_cents"] = 1000000001
		}, "custom_value_cents is invalid"},
		{"unknown pricing mode", func(m map[string]any) {
			m["pricing_mode"] = "bogus"
		}, "pricing_mode must be one of auto, proxy, custom, disabled"},
		{"entered pair missing its currency", func(m map[string]any) {
			m["pricing_mode"] = "custom"
			m["custom_value_cents"] = 5400
			m["custom_value_entered_cents"] = 6000
		}, "custom_value_entered_cents and custom_value_entered_currency must be provided together"},
		{"entered currency missing its cents", func(m map[string]any) {
			m["pricing_mode"] = "custom"
			m["custom_value_cents"] = 5400
			m["custom_value_entered_currency"] = "EUR"
		}, "custom_value_entered_cents and custom_value_entered_currency must be provided together"},
		{"entered pair without custom value", func(m map[string]any) {
			m["custom_value_entered_cents"] = 6000
			m["custom_value_entered_currency"] = "EUR"
		}, "custom_value_entered requires custom_value_cents"},
		{"entered cents negative", func(m map[string]any) {
			m["pricing_mode"] = "custom"
			m["custom_value_cents"] = 5400
			m["custom_value_entered_cents"] = -1
			m["custom_value_entered_currency"] = "EUR"
		}, "custom_value_entered_cents is invalid"},
		{"entered cents over cap", func(m map[string]any) {
			m["pricing_mode"] = "custom"
			m["custom_value_cents"] = 5400
			m["custom_value_entered_cents"] = 1000000001
			m["custom_value_entered_currency"] = "EUR"
		}, "custom_value_entered_cents is invalid"},
		{"entered currency malformed", func(m map[string]any) {
			m["pricing_mode"] = "custom"
			m["custom_value_cents"] = 5400
			m["custom_value_entered_cents"] = 6000
			m["custom_value_entered_currency"] = "eur"
		}, "custom_value_entered_currency is invalid"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			srv, a := newUnitServer(t, &stubStore{}, &stubEnrichment{}, newStubCache())
			resp := do(t, http.MethodPost, srv.URL+"/entries", a.token(t, uuid.NewString()),
				createBody(productID, tc.mutate))
			if resp.StatusCode != http.StatusBadRequest {
				t.Fatalf("status: got %d, want %d", resp.StatusCode, http.StatusBadRequest)
			}
			var p struct {
				Code   string `json:"code"`
				Detail string `json:"detail"`
			}
			if err := json.NewDecoder(resp.Body).Decode(&p); err != nil {
				t.Fatal(err)
			}
			if p.Code != "invalid_body" {
				t.Fatalf("code: got %q, want invalid_body", p.Code)
			}
			if p.Detail != tc.detail {
				t.Fatalf("detail: got %q, want %q", p.Detail, tc.detail)
			}
		})
	}
}

// TestUnitCustomPricing_MaxValueAccepted pins that the
// custom_value_cents cap is inclusive: exactly 1000000000 is a valid
// value, not a rejection.
func TestUnitCustomPricing_MaxValueAccepted(t *testing.T) {
	productID := uuid.New()
	st := &stubStore{createEntry: func(_ context.Context, e store.Entry, _ []uuid.UUID) (store.Entry, error) {
		e.ID = uuid.New()
		r := "n"
		e.BacklogRank = &r
		e.Tags = []store.TagRef{}
		e.CustomValueSetAt = new(time.Now())
		return e, nil
	}}
	enrich := &stubEnrichment{getProduct: func(_ context.Context, _ string, id uuid.UUID) (enrichapi.Product, error) {
		return gameProduct(id), nil
	}}
	srv, a := newUnitServer(t, st, enrich, newStubCache())
	resp := do(t, http.MethodPost, srv.URL+"/entries", a.token(t, uuid.NewString()),
		createBody(productID, func(m map[string]any) {
			m["pricing_mode"] = "custom"
			m["custom_value_cents"] = 1000000000
		}))
	if resp.StatusCode != http.StatusCreated {
		body, _ := io.ReadAll(resp.Body)
		t.Fatalf("status %d: %s", resp.StatusCode, body)
	}
	var got struct {
		CustomValueCents *int64 `json:"custom_value_cents"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&got); err != nil {
		t.Fatal(err)
	}
	if got.CustomValueCents == nil || *got.CustomValueCents != 1000000000 {
		t.Fatalf("custom_value_cents: %v", got.CustomValueCents)
	}
}

// TestUnitEntryValue_CustomModeShortCircuitsEnrichment pins the custom
// pricing short-circuit on both create and update: the composed value
// is always the stored cents, never a packaging-matched enrichment
// price, and enrichment is never even consulted for it.
func TestUnitEntryValue_CustomModeShortCircuitsEnrichment(t *testing.T) {
	productID := uuid.New()
	user := uuid.New()
	var created store.Entry
	st := &stubStore{
		createEntry: func(_ context.Context, e store.Entry, _ []uuid.UUID) (store.Entry, error) {
			e.ID = uuid.New()
			r := "n"
			e.BacklogRank = &r
			e.Tags = []store.TagRef{}
			e.CustomValueSetAt = new(time.Now()) // the store SQL stamps this server-side
			created = e
			return e, nil
		},
		getEntry: func(context.Context, uuid.UUID, uuid.UUID) (store.Entry, error) { return created, nil },
		updateEntry: func(_ context.Context, e store.Entry, _ []uuid.UUID) (store.Entry, error) {
			e.Tags = []store.TagRef{}
			return e, nil
		},
	}
	enrich := &stubEnrichment{
		getProduct: func(_ context.Context, _ string, id uuid.UUID) (enrichapi.Product, error) {
			return gameProduct(id), nil
		},
		batchPrices: func(context.Context, string, []uuid.UUID) (map[string]enrichapi.ProductPrices, error) {
			t.Fatal("enrichment must not be consulted for custom pricing")
			return nil, nil
		},
	}
	srv, a := newUnitServer(t, st, enrich, newStubCache())
	tok := a.token(t, user.String())

	resp := do(t, http.MethodPost, srv.URL+"/entries", tok,
		createBody(productID, func(m map[string]any) {
			m["pricing_mode"] = "custom"
			m["custom_value_cents"] = 12345
			m["packaging"] = "loose"
		}))
	if resp.StatusCode != http.StatusCreated {
		body, _ := io.ReadAll(resp.Body)
		t.Fatalf("status %d: %s", resp.StatusCode, body)
	}
	var got struct {
		ValueCents       *int64     `json:"value_cents"`
		CustomValueCents *int64     `json:"custom_value_cents"`
		CustomValueSetAt *time.Time `json:"custom_value_set_at"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&got); err != nil {
		t.Fatal(err)
	}
	if got.ValueCents == nil || *got.ValueCents != 12345 {
		t.Fatalf("value_cents: %v", got.ValueCents)
	}
	if got.CustomValueCents == nil || *got.CustomValueCents != 12345 {
		t.Fatalf("custom_value_cents: %v", got.CustomValueCents)
	}
	if got.CustomValueSetAt == nil {
		t.Fatal("custom_value_set_at must be set")
	}

	// PUT the full baseline with a different packaging: the value must
	// not move (packaging-independent under pricing_mode custom).
	resp = do(t, http.MethodPut, srv.URL+"/entries/"+created.ID.String(), tok,
		updateBody(func(m map[string]any) {
			m["packaging"] = "sealed"
			m["pricing_mode"] = "custom"
			m["custom_value_cents"] = 12345
		}))
	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		t.Fatalf("update status %d: %s", resp.StatusCode, body)
	}
	var got2 struct {
		ValueCents *int64 `json:"value_cents"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&got2); err != nil {
		t.Fatal(err)
	}
	if got2.ValueCents == nil || *got2.ValueCents != 12345 {
		t.Fatalf("value_cents after packaging change: %v", got2.ValueCents)
	}
}

// TestUnitEnteredPair_PassthroughOnCreate pins that the typed pair
// rides create -> store -> response untouched, next to the USD
// snapshot the backend actually computes with.
func TestUnitEnteredPair_PassthroughOnCreate(t *testing.T) {
	productID := uuid.New()
	var stored store.Entry
	st := &stubStore{
		createEntry: func(_ context.Context, e store.Entry, _ []uuid.UUID) (store.Entry, error) {
			stored = e
			e.ID = uuid.New()
			r := "n"
			e.BacklogRank = &r
			e.Tags = []store.TagRef{}
			e.CustomValueSetAt = new(time.Now())
			return e, nil
		},
	}
	enrich := &stubEnrichment{getProduct: func(_ context.Context, _ string, id uuid.UUID) (enrichapi.Product, error) {
		return gameProduct(id), nil
	}}
	srv, a := newUnitServer(t, st, enrich, newStubCache())
	resp := do(t, http.MethodPost, srv.URL+"/entries", a.token(t, uuid.NewString()),
		createBody(productID, func(m map[string]any) {
			m["pricing_mode"] = "custom"
			m["custom_value_cents"] = 5400
			m["custom_value_entered_cents"] = 6000
			m["custom_value_entered_currency"] = "EUR"
		}))
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("status: %d", resp.StatusCode)
	}
	if stored.CustomValueEnteredCents == nil || *stored.CustomValueEnteredCents != 6000 ||
		stored.CustomValueEnteredCurrency == nil || *stored.CustomValueEnteredCurrency != "EUR" {
		t.Fatalf("pair reaching the store: %+v %+v", stored.CustomValueEnteredCents, stored.CustomValueEnteredCurrency)
	}
	var got struct {
		EnteredCents    *int64  `json:"custom_value_entered_cents"`
		EnteredCurrency *string `json:"custom_value_entered_currency"`
		ValueCents      *int64  `json:"value_cents"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&got); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if got.EnteredCents == nil || *got.EnteredCents != 6000 || got.EnteredCurrency == nil || *got.EnteredCurrency != "EUR" {
		t.Fatalf("pair in the response: %+v %+v", got.EnteredCents, got.EnteredCurrency)
	}
	if got.ValueCents == nil || *got.ValueCents != 5400 {
		t.Fatalf("value_cents must stay the USD snapshot: %+v", got.ValueCents)
	}
}

// TestUnitCreateEntry_CustomOffCatalogWithCustomModeAndPCListingProxyBorrowsNothing
// pins two off-catalog corners: pricing_mode custom needs no product
// at all, and a pc_listing proxy target (like hardware) grants no
// game identity, since the existing nil-guard on target.Igdb already
// covers any anchor product that carries no igdb block.
func TestUnitCreateEntry_CustomOffCatalogWithCustomModeAndPCListingProxyBorrowsNothing(t *testing.T) {
	t.Run("off-catalog custom pricing needs no product", func(t *testing.T) {
		var stored store.Entry
		st := &stubStore{createEntry: func(_ context.Context, e store.Entry, _ []uuid.UUID) (store.Entry, error) {
			stored = e
			e.ID = uuid.New()
			r := "n"
			e.BacklogRank = &r
			e.Tags = []store.TagRef{}
			e.CustomValueSetAt = new(time.Now())
			return e, nil
		}}
		// Both enrichment fields deliberately nil: a call would panic.
		srv, a := newUnitServer(t, st, &stubEnrichment{}, newStubCache())
		resp := do(t, http.MethodPost, srv.URL+"/entries", a.token(t, uuid.NewString()),
			jsonBody(map[string]any{
				"display_name":       "Homebrew valued by owner",
				"item_type":          "game",
				"region":             "ntsc_u",
				"packaging":          "loose",
				"pricing_mode":       "custom",
				"custom_value_cents": 999,
			}))
		if resp.StatusCode != http.StatusCreated {
			body, _ := io.ReadAll(resp.Body)
			t.Fatalf("status %d: %s", resp.StatusCode, body)
		}
		if stored.ProductID != nil || stored.PricingMode != "custom" ||
			stored.CustomValueCents == nil || *stored.CustomValueCents != 999 {
			t.Fatalf("stored entry: %+v", stored)
		}
		var got struct {
			IgdbGameId *int64 `json:"igdb_game_id"`
			ValueCents *int64 `json:"value_cents"`
		}
		if err := json.NewDecoder(resp.Body).Decode(&got); err != nil {
			t.Fatal(err)
		}
		if got.IgdbGameId != nil {
			t.Fatalf("igdb_game_id must be absent: %v", *got.IgdbGameId)
		}
		if got.ValueCents == nil || *got.ValueCents != 999 {
			t.Fatalf("value_cents: %v", got.ValueCents)
		}
	})

	t.Run("pc_listing proxy grants no game identity", func(t *testing.T) {
		pcListingID := uuid.New()
		var stored store.Entry
		st := &stubStore{createEntry: func(_ context.Context, e store.Entry, _ []uuid.UUID) (store.Entry, error) {
			stored = e
			e.ID = uuid.New()
			r := "n"
			e.BacklogRank = &r
			e.Tags = []store.TagRef{}
			return e, nil
		}}
		enrich := &stubEnrichment{
			getProduct: func(_ context.Context, _ string, id uuid.UUID) (enrichapi.Product, error) {
				return enrichapi.Product{Id: id, Type: "pc_listing", Name: "eBay: repro cart"}, nil
			},
			batchPrices: pricedAs(1500, 4200, 9900),
		}
		srv, a := newUnitServer(t, st, enrich, newStubCache())
		resp := do(t, http.MethodPost, srv.URL+"/entries", a.token(t, uuid.NewString()),
			jsonBody(map[string]any{
				"display_name": "Homebrew cart", "item_type": "game",
				"region": "ntsc_u", "packaging": "loose",
				"pricing_mode": "proxy", "pricing_product_id": pcListingID.String(),
			}))
		if resp.StatusCode != http.StatusCreated {
			body, _ := io.ReadAll(resp.Body)
			t.Fatalf("status %d: %s", resp.StatusCode, body)
		}
		if stored.IGDBGameID != nil {
			t.Fatalf("pc_listing proxy target must grant no game identity: %v", *stored.IGDBGameID)
		}
		var got struct {
			IgdbGameId *int64 `json:"igdb_game_id"`
		}
		if err := json.NewDecoder(resp.Body).Decode(&got); err != nil {
			t.Fatal(err)
		}
		if got.IgdbGameId != nil {
			t.Fatalf("response igdb_game_id must be absent: %v", *got.IgdbGameId)
		}
	})
}

func TestCreateEntry_CommunityProductSnapshotFallbacks(t *testing.T) {
	userID := uuid.New()
	productID := uuid.New()
	platName := "SNES"
	rd := openapi_types.Date{Time: time.Date(1995, 10, 9, 0, 0, 0, 0, time.UTC)}
	community := enrichapi.Product{
		Id: productID, Type: "game", Name: "Repro Alpha",
		Community: &common.CommunityMeta{PlatformName: &platName, FirstReleaseDate: &rd},
	}
	var stored store.Entry
	st := &stubStore{createEntry: func(_ context.Context, e store.Entry, _ []uuid.UUID) (store.Entry, error) {
		stored = e
		e.ID = uuid.New()
		return e, nil
	}}
	enrich := &stubEnrichment{
		getProduct: func(context.Context, string, uuid.UUID) (enrichapi.Product, error) {
			return community, nil
		},
		// CreateEntry's response composes a value for the new
		// auto-priced, product-backed entry.
		batchPrices: pricedAs(1500, 4200, 9900),
	}
	srv, a := newUnitServer(t, st, enrich, newStubCache())

	resp := do(t, http.MethodPost, srv.URL+"/entries", a.token(t, userID.String()), jsonBody(map[string]any{
		"product_id": productID.String(), "region": "pal", "packaging": "loose",
	}))
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("create: %d", resp.StatusCode)
	}
	if stored.ProductID == nil || *stored.ProductID != productID {
		t.Fatalf("product id: %+v", stored.ProductID)
	}
	if stored.DisplayName != "Repro Alpha" || stored.ItemType != "game" {
		t.Fatalf("snapshot basics: %+v", stored)
	}
	if stored.PlatformName == nil || *stored.PlatformName != "SNES" || stored.PlatformIGDBID != nil {
		t.Fatalf("community platform fallback: name=%v id=%v", stored.PlatformName, stored.PlatformIGDBID)
	}
	if stored.FirstReleaseDate == nil || !stored.FirstReleaseDate.Equal(rd.Time) {
		t.Fatalf("community date fallback: %v", stored.FirstReleaseDate)
	}
	if stored.CoverURL != nil {
		t.Fatalf("community products have no cover: %v", stored.CoverURL)
	}
	if stored.IGDBGameID != nil {
		t.Fatalf("no igdb identity on community products: %v", stored.IGDBGameID)
	}
}

func TestEntryCustomCatalogFields_CoverAndPlatformId(t *testing.T) {
	userID := uuid.New()
	var saved store.Entry
	st := &stubStore{
		createEntry: func(_ context.Context, e store.Entry, _ []uuid.UUID) (store.Entry, error) {
			saved = e
			e.ID = uuid.New()
			return e, nil
		},
	}
	srv, a := newUnitServer(t, st, &stubEnrichment{}, newStubCache())
	bearer := a.token(t, userID.String())

	// Custom create persists both fields.
	body := `{"display_name":"Repro","item_type":"game","platform_name":"SNES","platform_igdb_id":19,` +
		`"cover_url":"https://img.example/r.jpg","region":"pal","packaging":"loose","pricing_mode":"disabled"}`
	resp := do(t, http.MethodPost, srv.URL+"/entries", bearer, strings.NewReader(body))
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("custom create: %d", resp.StatusCode)
	}
	if saved.CoverURL == nil || *saved.CoverURL != "https://img.example/r.jpg" ||
		saved.PlatformIGDBID == nil || *saved.PlatformIGDBID != 19 {
		t.Fatalf("custom fields not stored: %+v", saved)
	}

	// A non-https cover is rejected.
	bad := `{"display_name":"Repro","item_type":"game","cover_url":"http://x/y.jpg","region":"pal","packaging":"loose","pricing_mode":"disabled"}`
	resp = do(t, http.MethodPost, srv.URL+"/entries", bearer, strings.NewReader(bad))
	wantProblem(t, resp, http.StatusBadRequest, "invalid_body")

	// A product-backed create rejects cover_url / platform_igdb_id.
	pb := `{"product_id":"` + uuid.NewString() + `","cover_url":"https://img.example/z.jpg","region":"pal","packaging":"loose"}`
	resp = do(t, http.MethodPost, srv.URL+"/entries", bearer, strings.NewReader(pb))
	wantProblem(t, resp, http.StatusBadRequest, "invalid_body")
}

// TestUnitCreateEntry_PlatformIgdbIdRequiresPlatformName guards the
// platform pairing the DB enforces with
// CHECK(platform_igdb_id IS NULL OR platform_name IS NOT NULL): a
// custom body carrying platform_igdb_id with no usable platform_name
// must 400 in application validation, never reach the store to trip
// that constraint as a 500. createEntry stands in for the constraint
// itself - if validation ever lets this body through, the store
// answers the way the real violation would.
func TestUnitCreateEntry_PlatformIgdbIdRequiresPlatformName(t *testing.T) {
	st := &stubStore{createEntry: func(context.Context, store.Entry, []uuid.UUID) (store.Entry, error) {
		return store.Entry{}, errors.New(`pq: check constraint "products_platform_pairing" violated`)
	}}
	srv, a := newUnitServer(t, st, &stubEnrichment{}, newStubCache())
	bearer := a.token(t, uuid.NewString())

	body := `{"display_name":"Repro","item_type":"game","platform_igdb_id":19,` +
		`"region":"pal","packaging":"loose","pricing_mode":"disabled"}`
	resp := do(t, http.MethodPost, srv.URL+"/entries", bearer, strings.NewReader(body))
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("status: got %d, want 400", resp.StatusCode)
	}
	var p struct {
		Code   string `json:"code"`
		Detail string `json:"detail"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&p); err != nil {
		t.Fatal(err)
	}
	if p.Code != "invalid_body" {
		t.Fatalf("code: got %q, want invalid_body", p.Code)
	}
	if p.Detail != "platform_igdb_id requires platform_name" {
		t.Fatalf("detail: got %q", p.Detail)
	}
}

// Region edit on an auto-priced game entry whose member is cross-class
// re-resolves with the new region and repoints to the returned member;
// snapshot fields re-pick from the resolved payload.
func TestUpdateEntry_RegionChangeRepointsCrossClassAutoEntry(t *testing.T) {
	user := uuid.New()
	productID := uuid.New()
	siblingID := uuid.New()
	baseMember := pricedGameProduct(productID, "Super Nintendo") // base class
	jpDate := openapi_types.Date{Time: time.Date(1996, time.March, 6, 0, 0, 0, 0, time.UTC)}
	jpMember := pricedGameProduct(siblingID, "Super Famicom") // jp class: the region-correct sibling
	jpMember.Igdb.FirstReleaseDate = &jpDate

	var created store.Entry
	st := &stubStore{
		createEntry: func(_ context.Context, e store.Entry, _ []uuid.UUID) (store.Entry, error) {
			e.ID = uuid.New()
			r := "n"
			e.BacklogRank = &r
			e.Tags = []store.TagRef{}
			created = e
			return e, nil
		},
		getEntry: func(context.Context, uuid.UUID, uuid.UUID) (store.Entry, error) { return created, nil },
		updateEntry: func(_ context.Context, e store.Entry, _ []uuid.UUID) (store.Entry, error) {
			created = e
			e.Tags = []store.TagRef{}
			return e, nil
		},
	}
	var getProductCalls int
	var resolveCalls []enrichapi.ResolveRequest
	enrich := &stubEnrichment{
		getProduct: func(_ context.Context, _ string, id uuid.UUID) (enrichapi.Product, error) {
			getProductCalls++
			if id != productID {
				t.Fatalf("GetProduct called with unexpected id %s", id)
			}
			return baseMember, nil
		},
		resolve: func(_ context.Context, _ string, req enrichapi.ResolveRequest) (enrichapi.Product, error) {
			resolveCalls = append(resolveCalls, req)
			return jpMember, nil
		},
		batchPrices: pricedAs(1500, 4200, 9900),
	}
	srv, a := newUnitServer(t, st, enrich, newStubCache())
	tok := a.token(t, user.String())

	resp := do(t, http.MethodPost, srv.URL+"/entries", tok, createBody(productID, nil)) // region ntsc_u
	if resp.StatusCode != http.StatusCreated {
		body, _ := io.ReadAll(resp.Body)
		t.Fatalf("create status %d: %s", resp.StatusCode, body)
	}

	resp = do(t, http.MethodPut, srv.URL+"/entries/"+created.ID.String(), tok,
		updateBody(func(m map[string]any) { m["region"] = "ntsc_j" }))
	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		t.Fatalf("update status %d: %s", resp.StatusCode, body)
	}
	if getProductCalls != 2 {
		t.Fatalf("GetProduct calls: %d, want 2 (creation snapshot + the region arm's current-member fetch)", getProductCalls)
	}
	if len(resolveCalls) != 1 {
		t.Fatalf("resolve calls: %d, want 1", len(resolveCalls))
	}
	got := resolveCalls[0]
	if got.Type != "game" || got.IgdbGameId == nil || *got.IgdbGameId != 1000 ||
		got.PlatformIgdbId == nil || *got.PlatformIgdbId != 6 ||
		got.Region == nil || *got.Region != "ntsc_j" {
		t.Fatalf("resolve request: %+v", got)
	}
	if created.ProductID == nil || *created.ProductID != siblingID {
		t.Fatalf("must repoint to the resolved sibling: %v", created.ProductID)
	}
	want := time.Date(1996, time.March, 6, 0, 0, 0, 0, time.UTC)
	if created.FirstReleaseDate == nil || !created.FirstReleaseDate.Equal(want) {
		t.Fatalf("snapshot must re-pick from the resolved payload: %v", created.FirstReleaseDate)
	}
}

// Class-compatible members skip the resolve hop entirely (the stub
// asserts resolve was never called): an in-region manual variant pick
// survives a same-class region edit, and a ntsc_u -> region_free flip
// stays on the base member.
func TestUpdateEntry_RegionChangeSkipsClassCompatibleMember(t *testing.T) {
	user := uuid.New()

	t.Run("hand-chosen JP variant listing stays once the region edit lands in its class", func(t *testing.T) {
		productID := uuid.New()
		// A manual pick made while the entry still carried region
		// ntsc_u: the picker path ignores the passed region, so a JP
		// listing can already sit on a ntsc_u entry. The class guard -
		// not the region value at create time - decides whether a later
		// region edit re-resolves.
		jpVariant := pricedGameProduct(productID, "Super Famicom")
		var created store.Entry
		st := &stubStore{
			createEntry: func(_ context.Context, e store.Entry, _ []uuid.UUID) (store.Entry, error) {
				e.ID = uuid.New()
				r := "n"
				e.BacklogRank = &r
				e.Tags = []store.TagRef{}
				created = e
				return e, nil
			},
			getEntry: func(context.Context, uuid.UUID, uuid.UUID) (store.Entry, error) { return created, nil },
			updateEntry: func(_ context.Context, e store.Entry, _ []uuid.UUID) (store.Entry, error) {
				created = e
				e.Tags = []store.TagRef{}
				return e, nil
			},
		}
		var getProductCalls, resolveCalls int
		enrich := &stubEnrichment{
			getProduct: func(_ context.Context, _ string, id uuid.UUID) (enrichapi.Product, error) {
				getProductCalls++
				return jpVariant, nil
			},
			resolve: func(context.Context, string, enrichapi.ResolveRequest) (enrichapi.Product, error) {
				resolveCalls++
				return enrichapi.Product{}, nil
			},
			batchPrices: pricedAs(1500, 4200, 9900),
		}
		srv, a := newUnitServer(t, st, enrich, newStubCache())
		tok := a.token(t, user.String())

		resp := do(t, http.MethodPost, srv.URL+"/entries", tok, createBody(productID, nil))
		if resp.StatusCode != http.StatusCreated {
			body, _ := io.ReadAll(resp.Body)
			t.Fatalf("create status %d: %s", resp.StatusCode, body)
		}

		resp = do(t, http.MethodPut, srv.URL+"/entries/"+created.ID.String(), tok,
			updateBody(func(m map[string]any) { m["region"] = "ntsc_j" }))
		if resp.StatusCode != http.StatusOK {
			body, _ := io.ReadAll(resp.Body)
			t.Fatalf("update status %d: %s", resp.StatusCode, body)
		}
		if resolveCalls != 0 {
			t.Fatalf("class-compatible member must skip the resolve hop: %d calls", resolveCalls)
		}
		if getProductCalls != 2 {
			t.Fatalf("GetProduct calls: %d, want 2 (creation snapshot + the region arm's display re-pick)", getProductCalls)
		}
		if created.ProductID == nil || *created.ProductID != productID {
			t.Fatalf("must stay on the manually picked member: %v", created.ProductID)
		}
	})

	t.Run("ntsc_u to region_free flip stays on the base member", func(t *testing.T) {
		productID := uuid.New()
		baseMember := pricedGameProduct(productID, "Super Nintendo")
		var created store.Entry
		st := &stubStore{
			createEntry: func(_ context.Context, e store.Entry, _ []uuid.UUID) (store.Entry, error) {
				e.ID = uuid.New()
				r := "n"
				e.BacklogRank = &r
				e.Tags = []store.TagRef{}
				created = e
				return e, nil
			},
			getEntry: func(context.Context, uuid.UUID, uuid.UUID) (store.Entry, error) { return created, nil },
			updateEntry: func(_ context.Context, e store.Entry, _ []uuid.UUID) (store.Entry, error) {
				created = e
				e.Tags = []store.TagRef{}
				return e, nil
			},
		}
		var resolveCalls int
		enrich := &stubEnrichment{
			getProduct: func(_ context.Context, _ string, id uuid.UUID) (enrichapi.Product, error) { return baseMember, nil },
			resolve: func(context.Context, string, enrichapi.ResolveRequest) (enrichapi.Product, error) {
				resolveCalls++
				return enrichapi.Product{}, nil
			},
			batchPrices: pricedAs(1500, 4200, 9900),
		}
		srv, a := newUnitServer(t, st, enrich, newStubCache())
		tok := a.token(t, user.String())

		resp := do(t, http.MethodPost, srv.URL+"/entries", tok, createBody(productID, nil))
		if resp.StatusCode != http.StatusCreated {
			body, _ := io.ReadAll(resp.Body)
			t.Fatalf("create status %d: %s", resp.StatusCode, body)
		}

		resp = do(t, http.MethodPut, srv.URL+"/entries/"+created.ID.String(), tok,
			updateBody(func(m map[string]any) { m["region"] = "region_free" }))
		if resp.StatusCode != http.StatusOK {
			body, _ := io.ReadAll(resp.Body)
			t.Fatalf("update status %d: %s", resp.StatusCode, body)
		}
		if resolveCalls != 0 {
			t.Fatalf("base member must skip the resolve hop on a region_free flip: %d calls", resolveCalls)
		}
		if created.ProductID == nil || *created.ProductID != productID {
			t.Fatalf("must stay on the base member: %v", created.ProductID)
		}
	})
}

// A cross-class region change on a user-provenance entry re-picks
// display fields only: product_id unchanged, no Resolve call
// recorded on the stub.
func TestUpdateEntry_RegionChangeSkipsUserPick(t *testing.T) {
	user := uuid.New()
	productID := uuid.New()
	baseMember := pricedGameProduct(productID, "Super Nintendo") // base class: cross-class vs the ntsc_j target

	var created store.Entry
	st := &stubStore{
		createEntry: func(_ context.Context, e store.Entry, _ []uuid.UUID) (store.Entry, error) {
			e.ID = uuid.New()
			r := "n"
			e.BacklogRank = &r
			e.Tags = []store.TagRef{}
			created = e
			return e, nil
		},
		getEntry: func(context.Context, uuid.UUID, uuid.UUID) (store.Entry, error) { return created, nil },
		updateEntry: func(_ context.Context, e store.Entry, _ []uuid.UUID) (store.Entry, error) {
			created = e
			e.Tags = []store.TagRef{}
			return e, nil
		},
	}
	var getProductCalls, resolveCalls int
	enrich := &stubEnrichment{
		getProduct: func(_ context.Context, _ string, id uuid.UUID) (enrichapi.Product, error) {
			getProductCalls++
			return baseMember, nil
		},
		resolve: func(context.Context, string, enrichapi.ResolveRequest) (enrichapi.Product, error) {
			resolveCalls++
			return enrichapi.Product{}, nil
		},
		batchPrices: pricedAs(1500, 4200, 9900),
	}
	srv, a := newUnitServer(t, st, enrich, newStubCache())
	tok := a.token(t, user.String())

	resp := do(t, http.MethodPost, srv.URL+"/entries", tok,
		createBody(productID, func(m map[string]any) { m["match_provenance"] = "user" })) // region ntsc_u
	if resp.StatusCode != http.StatusCreated {
		body, _ := io.ReadAll(resp.Body)
		t.Fatalf("create status %d: %s", resp.StatusCode, body)
	}
	if created.MatchProvenance != "user" {
		t.Fatalf("precondition: created entry must carry match_provenance user, got %q", created.MatchProvenance)
	}

	resp = do(t, http.MethodPut, srv.URL+"/entries/"+created.ID.String(), tok,
		updateBody(func(m map[string]any) { m["region"] = "ntsc_j" }))
	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		t.Fatalf("update status %d: %s", resp.StatusCode, body)
	}
	if resolveCalls != 0 {
		t.Fatalf("a user-provenance entry must never resolve on a region edit: %d calls", resolveCalls)
	}
	if getProductCalls != 2 {
		t.Fatalf("GetProduct calls: %d, want 2 (creation snapshot + the region arm's display re-pick)", getProductCalls)
	}
	if created.ProductID == nil || *created.ProductID != productID {
		t.Fatalf("must stay on the user-picked product: %v", created.ProductID)
	}
}

// Non-auto entries never repoint on a region edit (display re-pick
// only), and the explicit product_id repoint arm outranks the region
// arm when both fire in one request.
func TestUpdateEntry_RegionChangeNonAutoAndExplicitRepointPrecedence(t *testing.T) {
	user := uuid.New()

	t.Run("non-auto entry re-picks display fields but never repoints", func(t *testing.T) {
		productID := uuid.New()
		// Deliberately cross-class (base vs jp): if pricing_mode gated
		// nothing, this shape would repoint. It must not, because
		// pricing_mode is disabled.
		baseMember := pricedGameProduct(productID, "Super Nintendo")
		// Two chain-eligible regions on the one product: the ntsc_u
		// creation snapshot and the ntsc_j region-arm re-pick below must
		// land different field values, proving the re-pick used the NEW
		// region rather than just replaying the creation-time snapshot.
		baseMember.Igdb.ReleaseDates = &[]common.ReleaseDate{
			{Region: "north_america", Date: openapi_types.Date{Time: time.Date(1991, time.August, 23, 0, 0, 0, 0, time.UTC)}},
			{Region: "japan", Date: openapi_types.Date{Time: time.Date(1990, time.January, 11, 0, 0, 0, 0, time.UTC)}},
		}
		baseMember.Igdb.Localizations = &[]common.Localization{
			{Region: "ja-JP", Name: new("聖剣伝説3"), Translit: new("Seiken Densetsu 3"), CoverUrl: new("https://images.igdb.example/jp.jpg")},
		}
		var created store.Entry
		st := &stubStore{
			createEntry: func(_ context.Context, e store.Entry, _ []uuid.UUID) (store.Entry, error) {
				e.ID = uuid.New()
				r := "n"
				e.BacklogRank = &r
				e.Tags = []store.TagRef{}
				created = e
				return e, nil
			},
			getEntry: func(context.Context, uuid.UUID, uuid.UUID) (store.Entry, error) { return created, nil },
			updateEntry: func(_ context.Context, e store.Entry, _ []uuid.UUID) (store.Entry, error) {
				created = e
				e.Tags = []store.TagRef{}
				return e, nil
			},
		}
		var getProductCalls, resolveCalls int
		enrich := &stubEnrichment{
			getProduct: func(_ context.Context, _ string, id uuid.UUID) (enrichapi.Product, error) {
				getProductCalls++
				return baseMember, nil
			},
			resolve: func(context.Context, string, enrichapi.ResolveRequest) (enrichapi.Product, error) {
				resolveCalls++
				return enrichapi.Product{}, nil
			},
		}
		srv, a := newUnitServer(t, st, enrich, newStubCache())
		tok := a.token(t, user.String())

		resp := do(t, http.MethodPost, srv.URL+"/entries", tok,
			createBody(productID, func(m map[string]any) { m["pricing_mode"] = "disabled" }))
		if resp.StatusCode != http.StatusCreated {
			body, _ := io.ReadAll(resp.Body)
			t.Fatalf("create status %d: %s", resp.StatusCode, body)
		}

		resp = do(t, http.MethodPut, srv.URL+"/entries/"+created.ID.String(), tok,
			updateBody(func(m map[string]any) {
				m["region"] = "ntsc_j"
				m["pricing_mode"] = "disabled"
			}))
		if resp.StatusCode != http.StatusOK {
			body, _ := io.ReadAll(resp.Body)
			t.Fatalf("update status %d: %s", resp.StatusCode, body)
		}
		if resolveCalls != 0 {
			t.Fatalf("non-auto entries must never resolve: %d calls", resolveCalls)
		}
		if getProductCalls != 2 {
			t.Fatalf("GetProduct calls: %d, want 2 (creation snapshot + the region arm's display re-pick)", getProductCalls)
		}
		if created.ProductID == nil || *created.ProductID != productID {
			t.Fatalf("non-auto entries must never repoint: %v", created.ProductID)
		}
		jpDate := time.Date(1990, time.January, 11, 0, 0, 0, 0, time.UTC)
		if created.FirstReleaseDate == nil || !created.FirstReleaseDate.Equal(jpDate) {
			t.Fatalf("display re-pick must use the new region's release date: %v", created.FirstReleaseDate)
		}
		if created.LocalizedName == nil || *created.LocalizedName != "聖剣伝説3" ||
			created.LocalizedNameTranslit == nil || *created.LocalizedNameTranslit != "Seiken Densetsu 3" ||
			created.LocalizedCoverURL == nil || *created.LocalizedCoverURL != "https://images.igdb.example/jp.jpg" {
			t.Fatalf("display re-pick must use the new region's localized bundle: %v %v %v",
				created.LocalizedName, created.LocalizedNameTranslit, created.LocalizedCoverURL)
		}
	})

	t.Run("explicit product_id repoint outranks the region arm", func(t *testing.T) {
		productID := uuid.New()
		target := uuid.New()
		curProd := gameProduct(productID) // unmatched: required for narrow re-match eligibility
		newProd := gameProduct(target)    // same family (game 1000, platform 6)
		var created store.Entry
		st := &stubStore{
			createEntry: func(_ context.Context, e store.Entry, _ []uuid.UUID) (store.Entry, error) {
				e.ID = uuid.New()
				r := "n"
				e.BacklogRank = &r
				e.Tags = []store.TagRef{}
				created = e
				return e, nil
			},
			getEntry: func(context.Context, uuid.UUID, uuid.UUID) (store.Entry, error) { return created, nil },
			updateEntry: func(_ context.Context, e store.Entry, _ []uuid.UUID) (store.Entry, error) {
				created = e
				e.Tags = []store.TagRef{}
				return e, nil
			},
		}
		var getProductCalls, resolveCalls int
		enrich := &stubEnrichment{
			getProduct: func(_ context.Context, _ string, id uuid.UUID) (enrichapi.Product, error) {
				getProductCalls++
				if id == productID {
					return curProd, nil
				}
				if id == target {
					return newProd, nil
				}
				return enrichapi.Product{}, enrichmentclient.ErrUnknownProduct
			},
			resolve: func(context.Context, string, enrichapi.ResolveRequest) (enrichapi.Product, error) {
				resolveCalls++
				return enrichapi.Product{}, nil
			},
			batchPrices: pricedAs(1500, 4200, 9900),
		}
		srv, a := newUnitServer(t, st, enrich, newStubCache())
		tok := a.token(t, user.String())

		resp := do(t, http.MethodPost, srv.URL+"/entries", tok, createBody(productID, nil)) // region ntsc_u
		if resp.StatusCode != http.StatusCreated {
			body, _ := io.ReadAll(resp.Body)
			t.Fatalf("create status %d: %s", resp.StatusCode, body)
		}

		resp = do(t, http.MethodPut, srv.URL+"/entries/"+created.ID.String(), tok,
			updateBody(func(m map[string]any) {
				m["product_id"] = target.String()
				m["region"] = "ntsc_j" // would itself be cross-class-eligible if reached
			}))
		if resp.StatusCode != http.StatusOK {
			body, _ := io.ReadAll(resp.Body)
			t.Fatalf("update status %d: %s", resp.StatusCode, body)
		}
		if resolveCalls != 0 {
			t.Fatalf("the explicit repoint must outrank the region arm: %d resolve calls", resolveCalls)
		}
		if getProductCalls != 3 {
			t.Fatalf("GetProduct calls: %d, want 3 (creation snapshot + repoint's current + repoint's new, no separate region-arm fetch)", getProductCalls)
		}
		if created.ProductID == nil || *created.ProductID != target {
			t.Fatalf("must land on the explicitly requested product: %v", created.ProductID)
		}
	})
}

// Enrichment down during the region-arm resolve answers 502
// enrichment_unavailable and leaves the entry unchanged.
func TestUpdateEntry_RegionChangeResolveOutageKeeps502Posture(t *testing.T) {
	user := uuid.New()
	productID := uuid.New()
	baseMember := pricedGameProduct(productID, "Super Nintendo") // base vs the ntsc_j target: cross-class

	var created store.Entry
	st := &stubStore{
		createEntry: func(_ context.Context, e store.Entry, _ []uuid.UUID) (store.Entry, error) {
			e.ID = uuid.New()
			r := "n"
			e.BacklogRank = &r
			e.Tags = []store.TagRef{}
			created = e
			return e, nil
		},
		getEntry: func(context.Context, uuid.UUID, uuid.UUID) (store.Entry, error) { return created, nil },
		// updateEntry deliberately nil: a resolve failure on the
		// region arm must return before any store write.
	}
	var resolveCalls int
	enrich := &stubEnrichment{
		getProduct: func(_ context.Context, _ string, id uuid.UUID) (enrichapi.Product, error) { return baseMember, nil },
		resolve: func(context.Context, string, enrichapi.ResolveRequest) (enrichapi.Product, error) {
			resolveCalls++
			return enrichapi.Product{}, enrichmentclient.ErrUnavailable
		},
		batchPrices: pricedAs(1500, 4200, 9900),
	}
	srv, a := newUnitServer(t, st, enrich, newStubCache())
	tok := a.token(t, user.String())

	resp := do(t, http.MethodPost, srv.URL+"/entries", tok, createBody(productID, nil)) // region ntsc_u
	if resp.StatusCode != http.StatusCreated {
		body, _ := io.ReadAll(resp.Body)
		t.Fatalf("create status %d: %s", resp.StatusCode, body)
	}

	resp = do(t, http.MethodPut, srv.URL+"/entries/"+created.ID.String(), tok,
		updateBody(func(m map[string]any) { m["region"] = "ntsc_j" }))
	wantProblem(t, resp, http.StatusBadGateway, "enrichment_unavailable")
	if resolveCalls != 1 {
		t.Fatalf("resolve calls: %d, want 1", resolveCalls)
	}
}
