package server_test

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/url"
	"reflect"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/levonn-dev/vgkeep/services/collection/internal/gen/api"
	"github.com/levonn-dev/vgkeep/services/collection/internal/store"
)

// TestSharedEntryWhitelist pins the projection: the SharedEntry
// contract type must expose exactly these JSON fields. A new field
// here is a privacy decision, not a convenience - update the spec
// section before touching this list.
func TestSharedEntryWhitelist(t *testing.T) {
	want := []string{
		"id", "product_id", "item_type", "media_type", "display_name",
		"platform", "first_release_date", "cover_url", "igdb_game_id",
		"localized_name", "localized_name_translit", "localized_cover_url",
		"region", "edition", "packaging", "has_box", "has_manual",
		"box_condition", "manual_condition", "item_condition",
		"pinned", "tags", "created_at",
	}
	typ := reflect.TypeFor[api.SharedEntry]()
	got := []string{}
	for field := range typ.Fields() {
		tag := field.Tag.Get("json")
		got = append(got, strings.Split(tag, ",")[0])
	}
	sort.Strings(want)
	sort.Strings(got)
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("SharedEntry fields = %v\nwant %v", got, want)
	}
}

func TestUnitSharedShelf_Gate(t *testing.T) {
	owner := uuid.New()
	shelf := store.View{ID: uuid.New(), UserID: owner, Name: "Backlog", Slug: "Backlog",
		Visibility: "unlisted", Params: []byte(`{"v":1}`)}

	t.Run("non-private resolves for a stranger", func(t *testing.T) {
		st := &stubStore{getSharedShelf: func(context.Context, uuid.UUID) (store.View, error) { return shelf, nil }}
		srv, a := newUnitServer(t, st, &stubEnrichment{}, newStubCache())
		resp := do(t, http.MethodGet, srv.URL+"/shared/shelves/"+shelf.ID.String(), a.token(t, uuid.New().String()), nil)
		if resp.StatusCode != http.StatusOK {
			t.Fatalf("status = %d", resp.StatusCode)
		}
		var got struct {
			OwnerID string `json:"owner_id"`
			Slug    string `json:"slug"`
		}
		_ = json.NewDecoder(resp.Body).Decode(&got)
		if got.OwnerID != owner.String() || got.Slug != "Backlog" {
			t.Fatalf("shelf = %+v", got)
		}
	})

	t.Run("private and missing are the same 404", func(t *testing.T) {
		priv := shelf
		priv.Visibility = "private"
		cases := map[string]*stubStore{
			"private": {getSharedShelf: func(context.Context, uuid.UUID) (store.View, error) { return priv, nil }},
			"missing": {getSharedShelf: func(context.Context, uuid.UUID) (store.View, error) { return store.View{}, store.ErrNotFound }},
		}
		for name, st := range cases {
			srv, a := newUnitServer(t, st, &stubEnrichment{}, newStubCache())
			resp := do(t, http.MethodGet, srv.URL+"/shared/shelves/"+uuid.NewString(), a.token(t, "viewer"), nil)
			if resp.StatusCode != http.StatusNotFound {
				t.Fatalf("%s: status = %d", name, resp.StatusCode)
			}
		}
	})
}

// TestUnitSharedShelfEntries_CarriesLocalizedSnapshot pins the
// localized trio on the public projection: a visitor reading someone
// else's shelf sees the same region-picked presentation the owner
// does. The whitelist above is the privacy side of this decision;
// this is the wiring side.
func TestUnitSharedShelfEntries_CarriesLocalizedSnapshot(t *testing.T) {
	owner := uuid.New()
	shelf := store.View{ID: uuid.New(), UserID: owner, Name: "Imports", Slug: "Imports",
		Visibility: "listed", Params: []byte(`{"v":1}`)}
	entry := store.Entry{ID: uuid.New(), ItemType: "game", MediaType: "physical",
		DisplayName: "Seiken Densetsu 3", Region: "ntsc_j", Packaging: "cib",
		Status: "beaten", CreatedAt: time.Now(), UpdatedAt: time.Now(),
		LocalizedName:         new("聖剣伝説3"),
		LocalizedNameTranslit: new("Seiken Densetsu 3"),
		LocalizedCoverURL:     new("https://images.igdb.example/jp.jpg"),
	}
	st := &stubStore{
		getSharedShelf: func(context.Context, uuid.UUID) (store.View, error) { return shelf, nil },
		listEntries: func(context.Context, uuid.UUID, store.Filters) ([]store.Entry, error) {
			return []store.Entry{entry}, nil
		},
	}
	srv, a := newUnitServer(t, st, &stubEnrichment{}, newStubCache())
	resp := do(t, http.MethodGet, srv.URL+"/shared/shelves/"+shelf.ID.String()+"/entries", a.token(t, "viewer"), nil)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d", resp.StatusCode)
	}
	var got struct {
		Entries []struct {
			LocalizedName         *string `json:"localized_name"`
			LocalizedNameTranslit *string `json:"localized_name_translit"`
			LocalizedCoverURL     *string `json:"localized_cover_url"`
		} `json:"entries"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&got); err != nil {
		t.Fatal(err)
	}
	if len(got.Entries) != 1 {
		t.Fatalf("entries = %d, want 1", len(got.Entries))
	}
	e := got.Entries[0]
	if e.LocalizedName == nil || *e.LocalizedName != "聖剣伝説3" ||
		e.LocalizedNameTranslit == nil || *e.LocalizedNameTranslit != "Seiken Densetsu 3" ||
		e.LocalizedCoverURL == nil || *e.LocalizedCoverURL != "https://images.igdb.example/jp.jpg" {
		t.Fatalf("shared entry localized snapshot: %v %v %v",
			e.LocalizedName, e.LocalizedNameTranslit, e.LocalizedCoverURL)
	}
}

func TestUnitSharedShelfEntries_ExecutesStoredParams(t *testing.T) {
	owner := uuid.New()
	shelf := store.View{ID: uuid.New(), UserID: owner, Name: "Backlog", Slug: "Backlog",
		Visibility: "listed",
		Params:     []byte(`{"v":1,"status":["backlog"],"sort":"backlog_rank","order":"asc","mode":"table","junk":"ignored"}`)}
	var gotFilters store.Filters
	var gotUser uuid.UUID
	entry := store.Entry{ID: uuid.New(), ItemType: "game", MediaType: "physical",
		DisplayName: "Chrono Trigger", Region: "ntsc_u", Packaging: "cib",
		Status: "backlog", PricePaidCents: new(int64(12345)), CreatedAt: time.Now(), UpdatedAt: time.Now()}
	st := &stubStore{
		getSharedShelf: func(context.Context, uuid.UUID) (store.View, error) { return shelf, nil },
		listEntries: func(_ context.Context, userID uuid.UUID, f store.Filters) ([]store.Entry, error) {
			gotUser, gotFilters = userID, f
			return []store.Entry{entry}, nil
		},
	}
	srv, a := newUnitServer(t, st, &stubEnrichment{}, newStubCache())
	resp := do(t, http.MethodGet, srv.URL+"/shared/shelves/"+shelf.ID.String()+"/entries", a.token(t, "viewer"), nil)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d", resp.StatusCode)
	}
	if gotUser != owner {
		t.Fatalf("executed as %s, want owner %s", gotUser, owner)
	}
	if len(gotFilters.Statuses) != 1 || gotFilters.Statuses[0] != "backlog" || gotFilters.Sort != "backlog_rank" || gotFilters.Order != "asc" {
		t.Fatalf("filters = %+v", gotFilters)
	}
	// The raw body must not contain any money or personal field name.
	raw, _ := io.ReadAll(resp.Body)
	for _, forbidden := range []string{"price_paid_cents", "value_cents", "custom_value", "status", "rating", "notes", "storage_location", "backlog_rank", "purchased", "pricing_mode", "currency"} {
		if strings.Contains(string(raw), `"`+forbidden+`"`) {
			t.Fatalf("projection leaked %q: %s", forbidden, raw)
		}
	}
}

// TestUnitSharedShelfEntries_StoredRegionFilterIsOpenWorld guards
// filtersFromViewParams' region dimension against regressing to a
// keep()-style allowlist gate like status/packaging/item_type use.
// region has no known-value set to gate against (open-world on the
// live list endpoint too), so a stored free-text value - one that was
// never a known region - must replay intact instead of silently
// vanishing. Sibling to TestUnitSharedShelfEntries_ExecutesStoredParams,
// isolating just this dimension.
func TestUnitSharedShelfEntries_StoredRegionFilterIsOpenWorld(t *testing.T) {
	owner := uuid.New()
	shelf := store.View{ID: uuid.New(), UserID: owner, Name: "Imports", Slug: "Imports",
		Visibility: "listed", Params: []byte(`{"v":1,"region":["Korea"]}`)}
	var gotFilters store.Filters
	st := &stubStore{
		getSharedShelf: func(context.Context, uuid.UUID) (store.View, error) { return shelf, nil },
		listEntries: func(_ context.Context, _ uuid.UUID, f store.Filters) ([]store.Entry, error) {
			gotFilters = f
			return []store.Entry{}, nil
		},
	}
	srv, a := newUnitServer(t, st, &stubEnrichment{}, newStubCache())
	resp := do(t, http.MethodGet, srv.URL+"/shared/shelves/"+shelf.ID.String()+"/entries", a.token(t, "viewer"), nil)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d", resp.StatusCode)
	}
	if len(gotFilters.Regions) != 1 || gotFilters.Regions[0] != "Korea" {
		t.Fatalf("Regions = %v, want [Korea] - an open-world region filter must survive the stored-params replay", gotFilters.Regions)
	}
}

// TestUnitSharedShelfEntries_PaginationValidation pins that offset/limit
// are validated against api/collection.yaml's bounds (limit 1-200,
// offset >= 0) before any store call, including the shelf lookup - the
// empty stubStore proves it. An unvalidated negative offset or
// out-of-range limit would otherwise panic the page slice.
func TestUnitSharedShelfEntries_PaginationValidation(t *testing.T) {
	srv, a := newUnitServer(t, &stubStore{}, &stubEnrichment{}, newStubCache())
	tok := a.token(t, "viewer")
	shelfID := uuid.NewString()
	for _, q := range []string{"offset=-1", "limit=0", "limit=201"} {
		resp := do(t, http.MethodGet, srv.URL+"/shared/shelves/"+shelfID+"/entries?"+q, tok, nil)
		wantProblem(t, resp, http.StatusBadRequest, "invalid_param")
	}
}

// TestUnitSharedShelfEntries_PaginationAtUpperBoundSucceeds pins the
// just-inside-bounds case: the yaml's declared max (200) must be
// accepted, not rejected as out of range.
func TestUnitSharedShelfEntries_PaginationAtUpperBoundSucceeds(t *testing.T) {
	owner := uuid.New()
	shelf := store.View{ID: uuid.New(), UserID: owner, Name: "Backlog", Slug: "Backlog",
		Visibility: "listed", Params: []byte(`{"v":1}`)}
	st := &stubStore{
		getSharedShelf: func(context.Context, uuid.UUID) (store.View, error) { return shelf, nil },
		listEntries: func(context.Context, uuid.UUID, store.Filters) ([]store.Entry, error) {
			return []store.Entry{}, nil
		},
	}
	srv, a := newUnitServer(t, st, &stubEnrichment{}, newStubCache())
	resp := do(t, http.MethodGet, srv.URL+"/shared/shelves/"+shelf.ID.String()+"/entries?limit=200&offset=0",
		a.token(t, "viewer"), nil)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("limit=200 (the yaml max) must be accepted: %d", resp.StatusCode)
	}
}

// TestUnitSharedShelfEntries_PaginationAtLowerBoundSucceeds pins the
// just-inside-bounds case at the other edge: limit=1 (the yaml's
// declared minimum) must be accepted, not rejected as out of range,
// and must actually constrain the page to one entry.
func TestUnitSharedShelfEntries_PaginationAtLowerBoundSucceeds(t *testing.T) {
	owner := uuid.New()
	shelf := store.View{ID: uuid.New(), UserID: owner, Name: "Backlog", Slug: "Backlog",
		Visibility: "listed", Params: []byte(`{"v":1}`)}
	st := &stubStore{
		getSharedShelf: func(context.Context, uuid.UUID) (store.View, error) { return shelf, nil },
		listEntries: func(context.Context, uuid.UUID, store.Filters) ([]store.Entry, error) {
			return []store.Entry{{ID: uuid.New()}, {ID: uuid.New()}}, nil
		},
	}
	srv, a := newUnitServer(t, st, &stubEnrichment{}, newStubCache())
	resp := do(t, http.MethodGet, srv.URL+"/shared/shelves/"+shelf.ID.String()+"/entries?limit=1&offset=0",
		a.token(t, "viewer"), nil)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("limit=1 (the yaml min) must be accepted: %d", resp.StatusCode)
	}
	var got struct {
		TotalCount int                `json:"total_count"`
		Entries    *[]api.SharedEntry `json:"entries"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&got); err != nil {
		t.Fatal(err)
	}
	if got.TotalCount != 2 || got.Entries == nil || len(*got.Entries) != 1 {
		t.Fatalf("limit=1 must return exactly one of the two entries: %+v", got)
	}
}

func TestUnitSharedShelfBySlug(t *testing.T) {
	owner := uuid.New()
	shelf := store.View{ID: uuid.New(), UserID: owner, Name: "Backlog", Slug: "Backlog",
		Visibility: "listed", Params: []byte(`{"v":1}`)}

	t.Run("found", func(t *testing.T) {
		var gotOwner uuid.UUID
		var gotSlug string
		st := &stubStore{getSharedShelfBySlug: func(_ context.Context, ownerID uuid.UUID, slug string) (store.View, error) {
			gotOwner, gotSlug = ownerID, slug
			return shelf, nil
		}}
		srv, a := newUnitServer(t, st, &stubEnrichment{}, newStubCache())
		resp := do(t, http.MethodGet, srv.URL+"/shared/shelves/by-slug?owner_id="+owner.String()+"&slug=Backlog",
			a.token(t, "viewer"), nil)
		if resp.StatusCode != http.StatusOK {
			t.Fatalf("status = %d", resp.StatusCode)
		}
		if gotOwner != owner || gotSlug != "backlog" {
			t.Fatalf("lookup args: owner=%s slug=%q", gotOwner, gotSlug)
		}
		var got struct {
			Id   string `json:"id"`
			Slug string `json:"slug"`
		}
		_ = json.NewDecoder(resp.Body).Decode(&got)
		if got.Id != shelf.ID.String() || got.Slug != "Backlog" {
			t.Fatalf("shelf = %+v", got)
		}
	})

	t.Run("missing slug is 404", func(t *testing.T) {
		st := &stubStore{getSharedShelfBySlug: func(context.Context, uuid.UUID, string) (store.View, error) {
			return store.View{}, store.ErrNotFound
		}}
		srv, a := newUnitServer(t, st, &stubEnrichment{}, newStubCache())
		resp := do(t, http.MethodGet, srv.URL+"/shared/shelves/by-slug?owner_id="+uuid.NewString()+"&slug=nope",
			a.token(t, "viewer"), nil)
		wantProblem(t, resp, http.StatusNotFound, "shelf_not_found")
	})

	t.Run("private shelf is the same 404 as missing", func(t *testing.T) {
		priv := shelf
		priv.Visibility = "private"
		st := &stubStore{getSharedShelfBySlug: func(context.Context, uuid.UUID, string) (store.View, error) {
			return priv, nil
		}}
		srv, a := newUnitServer(t, st, &stubEnrichment{}, newStubCache())
		resp := do(t, http.MethodGet, srv.URL+"/shared/shelves/by-slug?owner_id="+owner.String()+"&slug=Backlog",
			a.token(t, "viewer"), nil)
		wantProblem(t, resp, http.StatusNotFound, "shelf_not_found")
	})
}

func TestUnitListSharedShelves(t *testing.T) {
	owner := uuid.New()
	shelf := store.View{ID: uuid.New(), UserID: owner, Name: "Backlog", Slug: "Backlog",
		Visibility: "listed", Params: []byte(`{"v":1}`)}

	t.Run("returns summaries with count, covers, and limit/offset pass-through", func(t *testing.T) {
		var gotOwners []uuid.UUID
		var gotLimit, gotOffset int
		st := &stubStore{
			listListedShelves: func(_ context.Context, owners []uuid.UUID, limit, offset int) ([]store.View, int, error) {
				gotOwners, gotLimit, gotOffset = owners, limit, offset
				return []store.View{shelf}, 1, nil
			},
			countEntriesFiltered: func(context.Context, uuid.UUID, store.Filters) (int, error) { return 7, nil },
			coverURLs: func(context.Context, uuid.UUID, store.Filters, int) ([]string, error) {
				return []string{"https://a", "https://b"}, nil
			},
		}
		srv, a := newUnitServer(t, st, &stubEnrichment{}, newStubCache())
		resp := do(t, http.MethodGet, srv.URL+"/shared/shelves?owner_ids="+owner.String()+"&limit=5&offset=1",
			a.token(t, "viewer"), nil)
		if resp.StatusCode != http.StatusOK {
			t.Fatalf("status = %d", resp.StatusCode)
		}
		if len(gotOwners) != 1 || gotOwners[0] != owner || gotLimit != 5 || gotOffset != 1 {
			t.Fatalf("pass-through: owners=%v limit=%d offset=%d", gotOwners, gotLimit, gotOffset)
		}
		var got struct {
			TotalCount int `json:"total_count"`
			Shelves    []struct {
				Slug       string   `json:"slug"`
				EntryCount int      `json:"entry_count"`
				CoverUrls  []string `json:"cover_urls"`
			} `json:"shelves"`
		}
		_ = json.NewDecoder(resp.Body).Decode(&got)
		if got.TotalCount != 1 || len(got.Shelves) != 1 || got.Shelves[0].Slug != "Backlog" ||
			got.Shelves[0].EntryCount != 7 || len(got.Shelves[0].CoverUrls) != 2 {
			t.Fatalf("summary = %+v", got)
		}
	})

	// owner_ids-absent case: Explore-recent's read. No owners must
	// reach the store (nil, not a zero-length slice standing in for
	// "everyone") - store.ListListedShelves' own nil-slice contract
	// is what turns that into an unfiltered query.
	t.Run("owner_ids absent lists unfiltered across every owner", func(t *testing.T) {
		var gotOwners []uuid.UUID
		ownersArgSeen := false
		st := &stubStore{
			listListedShelves: func(_ context.Context, owners []uuid.UUID, limit, offset int) ([]store.View, int, error) {
				gotOwners, ownersArgSeen = owners, true
				return []store.View{shelf}, 1, nil
			},
			countEntriesFiltered: func(context.Context, uuid.UUID, store.Filters) (int, error) { return 4, nil },
			coverURLs:            func(context.Context, uuid.UUID, store.Filters, int) ([]string, error) { return nil, nil },
		}
		srv, a := newUnitServer(t, st, &stubEnrichment{}, newStubCache())
		resp := do(t, http.MethodGet, srv.URL+"/shared/shelves?limit=5", a.token(t, "viewer"), nil)
		if resp.StatusCode != http.StatusOK {
			t.Fatalf("status = %d", resp.StatusCode)
		}
		if !ownersArgSeen {
			t.Fatal("store.ListListedShelves was never called")
		}
		if len(gotOwners) != 0 {
			t.Fatalf("owners = %v, want none forwarded when owner_ids is absent", gotOwners)
		}
		var got struct {
			TotalCount int `json:"total_count"`
			Shelves    []struct {
				Slug string `json:"slug"`
			} `json:"shelves"`
		}
		_ = json.NewDecoder(resp.Body).Decode(&got)
		if got.TotalCount != 1 || len(got.Shelves) != 1 || got.Shelves[0].Slug != "Backlog" {
			t.Fatalf("summary = %+v", got)
		}
	})

	t.Run("validation", func(t *testing.T) {
		// The empty stubStore proves each guard answers 400 before any
		// store call.
		srv, a := newUnitServer(t, &stubStore{}, &stubEnrichment{}, newStubCache())
		tok := a.token(t, "viewer")
		validOwner := uuid.NewString()
		for _, q := range []string{
			"owner_ids=" + validOwner + "&offset=-1",
			"owner_ids=" + validOwner + "&limit=0",
			"owner_ids=" + validOwner + "&limit=101",
		} {
			resp := do(t, http.MethodGet, srv.URL+"/shared/shelves?"+q, tok, nil)
			wantProblem(t, resp, http.StatusBadRequest, "invalid_param")
		}
		// owner_ids over its maxItems bound (5000) gets its own code -
		// the generated param binder does not enforce maxItems.
		q := url.Values{}
		for range 5001 {
			q.Add("owner_ids", uuid.NewString())
		}
		resp := do(t, http.MethodGet, srv.URL+"/shared/shelves?"+q.Encode(), tok, nil)
		wantProblem(t, resp, http.StatusBadRequest, "too_many_owner_ids")
	})

	// TestUnitListSharedShelves at-bound acceptance: the yaml's declared
	// maxima (limit=100, owner_ids=5000) must be accepted, not rejected
	// as out of range - the mirror image of the "validation" subtest's
	// just-over-bound rejections above.
	t.Run("limit=100 (the yaml max) is accepted", func(t *testing.T) {
		var gotLimit int
		st := &stubStore{
			listListedShelves: func(_ context.Context, owners []uuid.UUID, limit, offset int) ([]store.View, int, error) {
				gotLimit = limit
				return nil, 0, nil
			},
		}
		srv, a := newUnitServer(t, st, &stubEnrichment{}, newStubCache())
		resp := do(t, http.MethodGet, srv.URL+"/shared/shelves?owner_ids="+uuid.NewString()+"&limit=100",
			a.token(t, "viewer"), nil)
		if resp.StatusCode != http.StatusOK {
			t.Fatalf("limit=100 (the yaml max) must be accepted: %d", resp.StatusCode)
		}
		if gotLimit != 100 {
			t.Fatalf("limit forwarded = %d, want 100", gotLimit)
		}
	})

	t.Run("owner_ids=5000 (the yaml max) is accepted", func(t *testing.T) {
		var gotOwners int
		st := &stubStore{
			listListedShelves: func(_ context.Context, owners []uuid.UUID, limit, offset int) ([]store.View, int, error) {
				gotOwners = len(owners)
				return nil, 0, nil
			},
		}
		srv, a := newUnitServer(t, st, &stubEnrichment{}, newStubCache())
		// url.Values.Add + Encode builds the query in linear time (a
		// single repeated key, no per-iteration string concatenation),
		// same idiom as the 5001-id rejection case above.
		q := url.Values{}
		for range 5000 {
			q.Add("owner_ids", uuid.NewString())
		}
		resp := do(t, http.MethodGet, srv.URL+"/shared/shelves?"+q.Encode(), a.token(t, "viewer"), nil)
		if resp.StatusCode != http.StatusOK {
			t.Fatalf("owner_ids=5000 (the yaml max) must be accepted: %d", resp.StatusCode)
		}
		if gotOwners != 5000 {
			t.Fatalf("owners forwarded = %d, want 5000", gotOwners)
		}
	})
}

func TestUnitGetSharedShelvesByIds(t *testing.T) {
	owner := uuid.New()
	known := store.View{ID: uuid.New(), UserID: owner, Name: "Backlog", Slug: "Backlog",
		Visibility: "listed", Params: []byte(`{"v":1}`)}

	t.Run("returns only the requested existing shelves; unknown ids silently absent", func(t *testing.T) {
		unknown := uuid.New()
		var gotIDs []uuid.UUID
		st := &stubStore{
			sharedShelvesByIDs: func(_ context.Context, ids []uuid.UUID) ([]store.View, error) {
				gotIDs = ids
				// The store itself already excludes private/missing ids;
				// the stub mirrors that by returning only the known one.
				return []store.View{known}, nil
			},
			countEntriesFiltered: func(context.Context, uuid.UUID, store.Filters) (int, error) { return 3, nil },
			coverURLs:            func(context.Context, uuid.UUID, store.Filters, int) ([]string, error) { return nil, nil },
		}
		srv, a := newUnitServer(t, st, &stubEnrichment{}, newStubCache())
		resp := do(t, http.MethodGet, srv.URL+"/shared/shelves/by-ids?ids="+known.ID.String()+"&ids="+unknown.String(),
			a.token(t, "viewer"), nil)
		if resp.StatusCode != http.StatusOK {
			t.Fatalf("status = %d", resp.StatusCode)
		}
		if len(gotIDs) != 2 {
			t.Fatalf("both requested ids must reach the store: %v", gotIDs)
		}
		var got struct {
			Shelves []struct {
				Id string `json:"id"`
			} `json:"shelves"`
		}
		_ = json.NewDecoder(resp.Body).Decode(&got)
		if len(got.Shelves) != 1 || got.Shelves[0].Id != known.ID.String() {
			t.Fatalf("want only the known shelf: %+v", got.Shelves)
		}
	})

	t.Run("too many ids is a 400 before the store is touched", func(t *testing.T) {
		q := url.Values{}
		for range 101 {
			q.Add("ids", uuid.NewString())
		}
		srv, a := newUnitServer(t, &stubStore{}, &stubEnrichment{}, newStubCache())
		resp := do(t, http.MethodGet, srv.URL+"/shared/shelves/by-ids?"+q.Encode(), a.token(t, "viewer"), nil)
		wantProblem(t, resp, http.StatusBadRequest, "too_many_ids")
	})

	t.Run("exactly the max (100 ids) is accepted", func(t *testing.T) {
		q := url.Values{}
		for range 100 {
			q.Add("ids", uuid.NewString())
		}
		st := &stubStore{sharedShelvesByIDs: func(context.Context, []uuid.UUID) ([]store.View, error) {
			return nil, nil
		}}
		srv, a := newUnitServer(t, st, &stubEnrichment{}, newStubCache())
		resp := do(t, http.MethodGet, srv.URL+"/shared/shelves/by-ids?"+q.Encode(), a.token(t, "viewer"), nil)
		if resp.StatusCode != http.StatusOK {
			t.Fatalf("100 ids (the yaml max) must be accepted: %d", resp.StatusCode)
		}
	})
}

// TestUnitSharedShelfSummaries_DropsPrivateDefenseInDepth pins
// shelfSummaries' own guard: even if a store path forgets to filter
// private (a bug, or a future caller), a private row must never reach
// shared output. Routed through GetSharedShelvesByIds since
// shelfSummaries itself is unexported.
func TestUnitSharedShelfSummaries_DropsPrivateDefenseInDepth(t *testing.T) {
	owner := uuid.New()
	listed := store.View{ID: uuid.New(), UserID: owner, Name: "Backlog", Slug: "Backlog",
		Visibility: "listed", Params: []byte(`{"v":1}`)}
	sneaky := store.View{ID: uuid.New(), UserID: owner, Name: "Secret", Slug: "Secret",
		Visibility: "private", Params: []byte(`{"v":1}`)}
	st := &stubStore{
		sharedShelvesByIDs: func(context.Context, []uuid.UUID) ([]store.View, error) {
			// Simulates a store bug that forgot the visibility filter.
			return []store.View{listed, sneaky}, nil
		},
		countEntriesFiltered: func(context.Context, uuid.UUID, store.Filters) (int, error) { return 1, nil },
		coverURLs:            func(context.Context, uuid.UUID, store.Filters, int) ([]string, error) { return nil, nil },
	}
	srv, a := newUnitServer(t, st, &stubEnrichment{}, newStubCache())
	resp := do(t, http.MethodGet, srv.URL+"/shared/shelves/by-ids?ids="+listed.ID.String()+"&ids="+sneaky.ID.String(),
		a.token(t, "viewer"), nil)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d", resp.StatusCode)
	}
	raw, _ := io.ReadAll(resp.Body)
	if strings.Contains(string(raw), "Secret") || strings.Contains(string(raw), sneaky.ID.String()) {
		t.Fatalf("a private shelf must never appear in shared output: %s", raw)
	}
	var got struct {
		Shelves []struct{ Id string } `json:"shelves"`
	}
	if err := json.Unmarshal(raw, &got); err != nil {
		t.Fatal(err)
	}
	if len(got.Shelves) != 1 || got.Shelves[0].Id != listed.ID.String() {
		t.Fatalf("want only the listed shelf: %+v", got.Shelves)
	}
}
