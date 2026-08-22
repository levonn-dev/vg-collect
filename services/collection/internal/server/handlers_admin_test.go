// Tests for admin and service-token levers.

package server_test

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	openapi_types "github.com/oapi-codegen/runtime/types"

	"github.com/levonn-dev/vgkeep/libs/go/contract/common"
	"github.com/levonn-dev/vgkeep/libs/go/contract/enrichapi"
	"github.com/levonn-dev/vgkeep/libs/go/reqtest"
	"github.com/levonn-dev/vgkeep/services/collection/internal/enrichmentclient"
	"github.com/levonn-dev/vgkeep/services/collection/internal/store"
)

func TestUnitPurgeUserData(t *testing.T) {
	user := uuid.New()

	t.Run("authorized purge answers 204 and calls the store with the token's sub", func(t *testing.T) {
		var gotUser uuid.UUID
		st := &stubStore{purgeUserData: func(_ context.Context, userID uuid.UUID) error {
			gotUser = userID
			return nil
		}}
		c := newStubCache()
		srv, a := newUnitServer(t, st, &stubEnrichment{}, c)
		resp := do(t, http.MethodDelete, srv.URL+"/user-data", a.token(t, user.String()), nil)
		if resp.StatusCode != http.StatusNoContent {
			t.Fatalf("status %d", resp.StatusCode)
		}
		if gotUser != user {
			t.Fatalf("store call: got user %v, want %v", gotUser, user)
		}
		// The dashboard cache was invalidated for exactly this user.
		if len(c.invalidated) != 1 || c.invalidated[0] != user.String() {
			t.Fatalf("invalidations: %v", c.invalidated)
		}
	})

	t.Run("store error is 500", func(t *testing.T) {
		st := &stubStore{purgeUserData: func(context.Context, uuid.UUID) error {
			return errors.New("boom")
		}}
		srv, a := newUnitServer(t, st, &stubEnrichment{}, newStubCache())
		resp := do(t, http.MethodDelete, srv.URL+"/user-data", a.token(t, user.String()), nil)
		wantProblem(t, resp, http.StatusInternalServerError, "internal")
	})
}

// ---- InternalResnapshot ----

// gameProductWithDates builds a game product carrying the given scalar
// first_release_date plus optional per-region IGDB dates, driving
// pickReleaseDate's region-chain resolution in the tests below.
func gameProductWithDates(id uuid.UUID, scalar time.Time, perRegion map[string]time.Time) enrichapi.Product {
	p := gameProduct(id)
	sc := openapi_types.Date{Time: scalar}
	p.Igdb.FirstReleaseDate = &sc
	if len(perRegion) > 0 {
		rows := make([]common.ReleaseDate, 0, len(perRegion))
		for region, when := range perRegion {
			rows = append(rows, common.ReleaseDate{Region: common.ReleaseRegion(region), Date: openapi_types.Date{Time: when}})
		}
		p.Igdb.ReleaseDates = &rows
	}
	return p
}

// TestUnitInternalResnapshot_HappyPath covers three entries across two
// products: one product carries two entries (regions ntsc_u and pal),
// the other carries one (region_free, no per-region rows so it falls
// back to the scalar). Only the rows whose recomputed pick differs
// from the stored date are written, and GetProduct is called exactly
// once per DISTINCT product, not once per entry.
func TestUnitInternalResnapshot_HappyPath(t *testing.T) {
	productA, productB := uuid.New(), uuid.New()
	entry1, entry2, entry3 := uuid.New(), uuid.New(), uuid.New()

	naDate := time.Date(1998, time.November, 21, 0, 0, 0, 0, time.UTC)
	euDate := time.Date(1998, time.December, 4, 0, 0, 0, 0, time.UTC)
	scalarB := time.Date(2001, time.June, 15, 0, 0, 0, 0, time.UTC)

	entry4 := uuid.New()
	refs := []store.GameEntryRef{
		// stale stored date -> the ntsc_u chain hit (north_america) differs, must update.
		{EntryID: entry1, ProductID: productA, Region: "ntsc_u", FirstReleaseDate: new(time.Date(1990, time.January, 1, 0, 0, 0, 0, time.UTC))},
		// stored date AND stored credits already match -> no write.
		{EntryID: entry2, ProductID: productA, Region: "pal", FirstReleaseDate: new(euDate),
			Developers: []string{"Square"}, Publishers: []string{"Square"}},
		// region_free has no chain, falls back to the scalar; unset -> must update.
		{EntryID: entry3, ProductID: productB, Region: "region_free", FirstReleaseDate: nil},
		// date matches but the stored credits are stale -> the credit
		// half of the diff alone must force the write.
		{EntryID: entry4, ProductID: productA, Region: "pal", FirstReleaseDate: new(euDate),
			Developers: []string{"Stale Studio"}, Publishers: []string{"Square"}},
	}

	var mu sync.Mutex
	productCalls := map[uuid.UUID]int{}
	updated := map[uuid.UUID]*time.Time{}
	updatedDevs := map[uuid.UUID][]string{}

	st := &stubStore{
		listGameBackedRefs: func(context.Context) ([]store.GameEntryRef, error) { return refs, nil },
		setSnapshotFields: func(_ context.Context, entryID uuid.UUID, d *time.Time, _, _, _ *string, developers, _ []string) error {
			mu.Lock()
			defer mu.Unlock()
			updated[entryID] = d
			updatedDevs[entryID] = developers
			return nil
		},
	}
	enrich := &stubEnrichment{
		getProduct: func(_ context.Context, _ string, id uuid.UUID) (enrichapi.Product, error) {
			mu.Lock()
			productCalls[id]++
			mu.Unlock()
			switch id {
			case productA:
				p := gameProductWithDates(id, time.Date(1998, time.January, 1, 0, 0, 0, 0, time.UTC),
					map[string]time.Time{"north_america": naDate, "europe": euDate})
				p.Igdb.Companies = []common.CompanyCredit{{Name: "Square", Developer: true, Publisher: true}}
				return p, nil
			case productB:
				return gameProductWithDates(id, scalarB, nil), nil
			default:
				t.Fatalf("unexpected product id %s", id)
				return enrichapi.Product{}, nil
			}
		},
	}
	srv, a := newUnitServer(t, st, enrich, newStubCache())

	resp := do(t, http.MethodPost, srv.URL+"/internal/resnapshot", a.token(t, uuid.NewString(), "admin"), nil)
	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		t.Fatalf("status %d: %s", resp.StatusCode, body)
	}
	var got struct {
		ProductsSeen   int `json:"products_seen"`
		ProductsFailed int `json:"products_failed"`
		EntriesUpdated int `json:"entries_updated"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&got); err != nil {
		t.Fatal(err)
	}
	if got.ProductsSeen != 2 || got.ProductsFailed != 0 || got.EntriesUpdated != 3 {
		t.Fatalf("counts: %+v", got)
	}
	if len(productCalls) != 2 || productCalls[productA] != 1 || productCalls[productB] != 1 {
		t.Fatalf("GetProduct must be called exactly once per distinct product: %v", productCalls)
	}
	if len(updated) != 3 {
		t.Fatalf("only changed rows must be written: %v", updated)
	}
	if d := updated[entry1]; d == nil || !d.Equal(naDate) {
		t.Fatalf("entry1 pick: %v", d)
	}
	if _, ok := updated[entry2]; ok {
		t.Fatal("entry2 matches on date and credits alike and must not be rewritten")
	}
	if d := updated[entry3]; d == nil || !d.Equal(scalarB) {
		t.Fatalf("entry3 pick: %v", d)
	}
	if devs := updatedDevs[entry4]; len(devs) != 1 || devs[0] != "Square" {
		t.Fatalf("entry4 must be rewritten for its stale credits alone, got developers %v", devs)
	}
}

// TestUnitInternalResnapshot_FailedProductIsPartialProgress covers a
// product fetch failure: the failed product counts against
// products_failed and its entries are left untouched, while the other
// product's entries still update (partial progress, not an
// all-or-nothing walk).
func TestUnitInternalResnapshot_FailedProductIsPartialProgress(t *testing.T) {
	productC, productD := uuid.New(), uuid.New()
	entry4, entry5 := uuid.New(), uuid.New()
	dDate := time.Date(2002, time.May, 3, 0, 0, 0, 0, time.UTC)

	refs := []store.GameEntryRef{
		{EntryID: entry4, ProductID: productC, Region: "ntsc_u", FirstReleaseDate: nil},
		{EntryID: entry5, ProductID: productD, Region: "region_free", FirstReleaseDate: nil},
	}
	var mu sync.Mutex
	updated := map[uuid.UUID]bool{}
	st := &stubStore{
		listGameBackedRefs: func(context.Context) ([]store.GameEntryRef, error) { return refs, nil },
		setSnapshotFields: func(_ context.Context, entryID uuid.UUID, _ *time.Time, _, _, _ *string, _, _ []string) error {
			mu.Lock()
			updated[entryID] = true
			mu.Unlock()
			return nil
		},
	}
	enrich := &stubEnrichment{
		getProduct: func(_ context.Context, _ string, id uuid.UUID) (enrichapi.Product, error) {
			if id == productC {
				return enrichapi.Product{}, enrichmentclient.ErrUnavailable
			}
			return gameProductWithDates(id, dDate, nil), nil
		},
	}
	srv, a := newUnitServer(t, st, enrich, newStubCache())
	resp := do(t, http.MethodPost, srv.URL+"/internal/resnapshot", a.token(t, uuid.NewString(), "admin"), nil)
	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		t.Fatalf("status %d: %s", resp.StatusCode, body)
	}
	var got struct {
		ProductsSeen   int `json:"products_seen"`
		ProductsFailed int `json:"products_failed"`
		EntriesUpdated int `json:"entries_updated"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&got); err != nil {
		t.Fatal(err)
	}
	if got.ProductsSeen != 2 || got.ProductsFailed != 1 || got.EntriesUpdated != 1 {
		t.Fatalf("counts: %+v", got)
	}
	if updated[entry4] {
		t.Fatal("the failed product's entry must not be written")
	}
	if !updated[entry5] {
		t.Fatal("the other product's entry must still be updated (partial progress)")
	}
}

// TestUnitInternalResnapshot_Idempotent runs the handler twice against
// a stub whose stored state reflects the first run's write: the
// second run must recompute the identical pick and write nothing.
func TestUnitInternalResnapshot_Idempotent(t *testing.T) {
	productE := uuid.New()
	entry6 := uuid.New()
	eDate := time.Date(2003, time.September, 9, 0, 0, 0, 0, time.UTC)

	var mu sync.Mutex
	var stored *time.Time // starts unset; the walk fills it on the first run
	st := &stubStore{
		listGameBackedRefs: func(context.Context) ([]store.GameEntryRef, error) {
			mu.Lock()
			defer mu.Unlock()
			return []store.GameEntryRef{{EntryID: entry6, ProductID: productE, Region: "ntsc_u", FirstReleaseDate: stored}}, nil
		},
		setSnapshotFields: func(_ context.Context, entryID uuid.UUID, d *time.Time, _, _, _ *string, _, _ []string) error {
			mu.Lock()
			defer mu.Unlock()
			if entryID != entry6 {
				t.Fatalf("unexpected entry %s", entryID)
			}
			stored = d
			return nil
		},
	}
	enrich := &stubEnrichment{
		getProduct: func(_ context.Context, _ string, id uuid.UUID) (enrichapi.Product, error) {
			return gameProductWithDates(id, eDate, nil), nil
		},
	}
	srv, a := newUnitServer(t, st, enrich, newStubCache())
	tok := a.token(t, uuid.NewString(), "admin")

	first := do(t, http.MethodPost, srv.URL+"/internal/resnapshot", tok, nil)
	var got1 struct {
		EntriesUpdated int `json:"entries_updated"`
	}
	if err := json.NewDecoder(first.Body).Decode(&got1); err != nil {
		t.Fatal(err)
	}
	if got1.EntriesUpdated != 1 {
		t.Fatalf("first run must write the unset row: %+v", got1)
	}

	second := do(t, http.MethodPost, srv.URL+"/internal/resnapshot", tok, nil)
	var got2 struct {
		EntriesUpdated int `json:"entries_updated"`
	}
	if err := json.NewDecoder(second.Body).Decode(&got2); err != nil {
		t.Fatal(err)
	}
	if got2.EntriesUpdated != 0 {
		t.Fatalf("second run must be a no-op once the stored date reflects the pick: %+v", got2)
	}
}

// TestUnitInternalResnapshot_LocalizedTrio covers the localized side
// of the walk: two entries share one product, one ntsc_j and one
// ntsc_u, and the product carries a ja-JP localization bundle. Both
// entries' stored date already matches the pick (the product sets no
// per-region release dates, so both regions fall back to the same
// scalar), which isolates the write trigger to the localization diff
// alone. A second run against the now-backfilled state is a no-op.
func TestUnitInternalResnapshot_LocalizedTrio(t *testing.T) {
	productF := uuid.New()
	entryJ, entryU := uuid.New(), uuid.New()
	scalar := time.Date(1995, time.March, 11, 0, 0, 0, 0, time.UTC) // matches gameProduct's default

	prod := gameProduct(productF)
	jaName, jaTranslit, jaCover := "クロノ・トリガー", "Kurono Torigaa", "https://x/ja-cover.jpg"
	prod.Igdb.Localizations = &[]common.Localization{
		{Region: "ja-JP", Name: &jaName, Translit: &jaTranslit, CoverUrl: &jaCover},
	}

	var mu sync.Mutex
	storedJ := store.GameEntryRef{EntryID: entryJ, ProductID: productF, Region: "ntsc_j", FirstReleaseDate: &scalar}
	storedU := store.GameEntryRef{EntryID: entryU, ProductID: productF, Region: "ntsc_u", FirstReleaseDate: &scalar}

	st := &stubStore{
		listGameBackedRefs: func(context.Context) ([]store.GameEntryRef, error) {
			mu.Lock()
			defer mu.Unlock()
			return []store.GameEntryRef{storedJ, storedU}, nil
		},
		setSnapshotFields: func(_ context.Context, entryID uuid.UUID, d *time.Time, name, translit, cover *string, developers, publishers []string) error {
			mu.Lock()
			defer mu.Unlock()
			switch entryID {
			case entryJ:
				storedJ.FirstReleaseDate, storedJ.LocalizedName = d, name
				storedJ.LocalizedNameTranslit, storedJ.LocalizedCoverURL = translit, cover
			case entryU:
				storedU.FirstReleaseDate, storedU.LocalizedName = d, name
				storedU.LocalizedNameTranslit, storedU.LocalizedCoverURL = translit, cover
			default:
				t.Fatalf("unexpected entry %s", entryID)
			}
			return nil
		},
	}
	enrich := &stubEnrichment{
		getProduct: func(_ context.Context, _ string, id uuid.UUID) (enrichapi.Product, error) {
			if id != productF {
				t.Fatalf("unexpected product id %s", id)
			}
			return prod, nil
		},
	}
	srv, a := newUnitServer(t, st, enrich, newStubCache())
	tok := a.token(t, uuid.NewString(), "admin")

	first := do(t, http.MethodPost, srv.URL+"/internal/resnapshot", tok, nil)
	var got1 struct {
		ProductsSeen   int `json:"products_seen"`
		ProductsFailed int `json:"products_failed"`
		EntriesUpdated int `json:"entries_updated"`
	}
	if err := json.NewDecoder(first.Body).Decode(&got1); err != nil {
		t.Fatal(err)
	}
	if got1.ProductsSeen != 1 || got1.ProductsFailed != 0 || got1.EntriesUpdated != 1 {
		t.Fatalf("first run counts: %+v", got1)
	}

	mu.Lock()
	gotJ, gotU := storedJ, storedU
	mu.Unlock()
	if gotJ.LocalizedName == nil || *gotJ.LocalizedName != jaName {
		t.Fatalf("ntsc_j must gain the localized name: %v", gotJ.LocalizedName)
	}
	if gotJ.LocalizedNameTranslit == nil || *gotJ.LocalizedNameTranslit != jaTranslit {
		t.Fatalf("ntsc_j must gain the transliteration: %v", gotJ.LocalizedNameTranslit)
	}
	if gotJ.LocalizedCoverURL == nil || *gotJ.LocalizedCoverURL != jaCover {
		t.Fatalf("ntsc_j must gain the localized cover: %v", gotJ.LocalizedCoverURL)
	}
	if gotU.LocalizedName != nil || gotU.LocalizedNameTranslit != nil || gotU.LocalizedCoverURL != nil {
		t.Fatalf("ntsc_u has no localization chain and must stay untouched: %+v", gotU)
	}

	second := do(t, http.MethodPost, srv.URL+"/internal/resnapshot", tok, nil)
	var got2 struct {
		EntriesUpdated int `json:"entries_updated"`
	}
	if err := json.NewDecoder(second.Body).Decode(&got2); err != nil {
		t.Fatal(err)
	}
	if got2.EntriesUpdated != 0 {
		t.Fatalf("second run must be a no-op once the trio is backfilled: %+v", got2)
	}
}

// A plain user bearer is refused on resnapshot now (tightened from
// any valid JWT): an all-nil stub store/enrichment panics if the
// handler ever reaches past the guard, so a silent bypass fails
// loudly rather than passing.
func TestUnitInternalResnapshot_NonAdminOrServiceIsForbidden(t *testing.T) {
	srv, a := newUnitServer(t, &stubStore{}, &stubEnrichment{}, newStubCache())
	resp := do(t, http.MethodPost, srv.URL+"/internal/resnapshot", a.token(t, uuid.NewString()), nil)
	wantProblem(t, resp, http.StatusForbidden, "forbidden")
}

// A service token passes the admin-or-service guard exactly like an
// admin bearer does.
func TestUnitInternalResnapshot_ServiceTokenIsAccepted(t *testing.T) {
	st := &stubStore{listGameBackedRefs: func(context.Context) ([]store.GameEntryRef, error) { return nil, nil }}
	srv, a := newUnitServer(t, st, &stubEnrichment{}, newStubCache())
	resp := do(t, http.MethodPost, srv.URL+"/internal/resnapshot", a.serviceToken(t, "svc:entry-rematch"), nil)
	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		t.Fatalf("status %d: %s", resp.StatusCode, body)
	}
}

// ---- InternalRematchEntries ----

// triggerRematch fires the entry rematch trigger and asserts the 202
// the detached run answers with; the counts trio that used to ride
// this response now lands in the completion log and the rematch.*
// metrics instead (TestUnitRematchMetrics in telemetry_test.go pins
// those).
func triggerRematch(t *testing.T, srv *httptest.Server, bearer string) {
	t.Helper()
	resp := do(t, http.MethodPost, srv.URL+"/internal/rematch-entries", bearer, nil)
	if resp.StatusCode != http.StatusAccepted {
		body, _ := io.ReadAll(resp.Body)
		t.Fatalf("status %d: %s", resp.StatusCode, body)
	}
}

// The rematch groups entries into (game, platform, region) triples,
// resolves once per triple that has a non-compatible entry, repoints
// only those entries, and detaches (202): the count trio that used to
// ride the response now lands in the completion log and the
// rematch.* metrics instead (TestUnitRematchMetrics). Second run is a
// no-op (idempotence).
func TestInternalRematchEntries_RepointsAndIsIdempotent(t *testing.T) {
	productBase := uuid.New()
	productJP := uuid.New()
	entry1, entry2 := uuid.New(), uuid.New()

	baseMember := pricedGameProduct(productBase, "Super Nintendo") // base class
	jpDate := openapi_types.Date{Time: time.Date(1996, time.March, 6, 0, 0, 0, 0, time.UTC)}
	jpMember := pricedGameProduct(productJP, "Super Famicom") // jp class: the region-correct sibling
	jpMember.Igdb.FirstReleaseDate = &jpDate

	var mu sync.Mutex
	// Both entries share one (game, platform, region) triple and start
	// on the base-class member; RepointEntry mutates this slice in
	// place so a second run sees the post-repoint state, exactly like
	// the store's real UPDATE would.
	refs := []store.RematchEntryRef{
		{EntryID: entry1, ProductID: productBase, IGDBGameID: 1000, PlatformIGDBID: 6, Region: "ntsc_j"},
		{EntryID: entry2, ProductID: productBase, IGDBGameID: 1000, PlatformIGDBID: 6, Region: "ntsc_j"},
	}
	var resolveCalls []enrichapi.ResolveRequest
	var getProductCalls int
	st := &stubStore{
		listAutoGameRematchRefs: func(context.Context) ([]store.RematchEntryRef, error) {
			mu.Lock()
			defer mu.Unlock()
			out := make([]store.RematchEntryRef, len(refs))
			copy(out, refs)
			return out, nil
		},
		repointEntry: func(_ context.Context, entryID, productID uuid.UUID, d *time.Time, name, translit, cover *string, developers, publishers []string) error {
			mu.Lock()
			defer mu.Unlock()
			for i := range refs {
				if refs[i].EntryID == entryID {
					refs[i].ProductID = productID
					refs[i].FirstReleaseDate = d
					refs[i].LocalizedName, refs[i].LocalizedNameTranslit, refs[i].LocalizedCoverURL = name, translit, cover
				}
			}
			return nil
		},
	}
	enrich := &stubEnrichment{
		getProduct: func(_ context.Context, _ string, id uuid.UUID) (enrichapi.Product, error) {
			mu.Lock()
			getProductCalls++
			mu.Unlock()
			switch id {
			case productBase:
				return baseMember, nil
			case productJP:
				return jpMember, nil
			default:
				t.Fatalf("unexpected product id %s", id)
				return enrichapi.Product{}, nil
			}
		},
		resolve: func(_ context.Context, _ string, req enrichapi.ResolveRequest) (enrichapi.Product, error) {
			mu.Lock()
			resolveCalls = append(resolveCalls, req)
			mu.Unlock()
			return jpMember, nil
		},
	}
	srv, a := newUnitServer(t, st, enrich, newStubCache())
	tok := a.token(t, uuid.NewString(), "admin")

	triggerRematch(t, srv, tok)
	reqtest.WaitFor(t, 5*time.Second, func() bool {
		mu.Lock()
		defer mu.Unlock()
		return refs[0].ProductID == productJP && refs[1].ProductID == productJP
	})

	mu.Lock()
	if len(resolveCalls) != 1 {
		mu.Unlock()
		t.Fatalf("resolve must be called exactly once per triple, not per entry: %d calls", len(resolveCalls))
	}
	gotReq := resolveCalls[0]
	r1, r2 := refs[0], refs[1]
	mu.Unlock()
	if gotReq.Type != "game" || gotReq.IgdbGameId == nil || *gotReq.IgdbGameId != 1000 ||
		gotReq.PlatformIgdbId == nil || *gotReq.PlatformIgdbId != 6 ||
		gotReq.Region == nil || *gotReq.Region != "ntsc_j" {
		t.Fatalf("resolve request: %+v", gotReq)
	}
	if r1.ProductID != productJP || r2.ProductID != productJP {
		t.Fatalf("both entries must repoint to the resolved sibling: %+v / %+v", r1, r2)
	}
	if r1.FirstReleaseDate == nil || !r1.FirstReleaseDate.Equal(jpDate.Time) {
		t.Fatalf("snapshot must re-pick from the resolved payload: %v", r1.FirstReleaseDate)
	}

	// Second run: both entries now sit on the class-compatible jp
	// member, so neither is pending - no resolve, no repoint. The
	// member fetch is this run's only remaining call before that
	// decision, so its second cumulative firing proves the run reached
	// (and, since nothing async follows within the triple, finished)
	// the no-op path.
	triggerRematch(t, srv, tok)
	reqtest.WaitFor(t, 5*time.Second, func() bool {
		mu.Lock()
		defer mu.Unlock()
		return getProductCalls == 2
	})
	mu.Lock()
	defer mu.Unlock()
	if len(resolveCalls) != 1 {
		t.Fatalf("second run must be a no-op once every entry sits on a class-compatible member: %d resolve calls, want still 1", len(resolveCalls))
	}
}

// Per-entry guard inside a shared triple: an entry on a
// class-compatible manual member survives while its neighbor on the
// unmatched member repoints; non-auto entries are never listed.
func TestInternalRematchEntries_ClassGuardIsPerEntry(t *testing.T) {
	productManualJP := uuid.New() // entry1's hand-picked, already region-correct member
	productUnmatched := uuid.New()
	entry1, entry2 := uuid.New(), uuid.New()

	manualJP := pricedGameProduct(productManualJP, "Super Famicom") // jp class: already correct for ntsc_j
	unmatched := gameProduct(productUnmatched)                      // no pricecharting -> never region-correct

	// Both entries share one (game, platform, region) triple. A third,
	// non-auto entry on the very same triple is deliberately absent
	// from this list: ListAutoGameRematchRefs' pricing_mode = 'auto'
	// filter (TestListAutoGameRematchRefs) keeps it out before the
	// handler ever sees it.
	refs := []store.RematchEntryRef{
		{EntryID: entry1, ProductID: productManualJP, IGDBGameID: 1000, PlatformIGDBID: 6, Region: "ntsc_j"},
		{EntryID: entry2, ProductID: productUnmatched, IGDBGameID: 1000, PlatformIGDBID: 6, Region: "ntsc_j"},
	}
	var mu sync.Mutex
	var repointed []uuid.UUID
	var resolveCalls int
	getProductCalls := map[uuid.UUID]int{}
	st := &stubStore{
		listAutoGameRematchRefs: func(context.Context) ([]store.RematchEntryRef, error) { return refs, nil },
		repointEntry: func(_ context.Context, entryID, productID uuid.UUID, _ *time.Time, _, _, _ *string, _, _ []string) error {
			mu.Lock()
			defer mu.Unlock()
			if productID != productManualJP {
				t.Fatalf("must repoint onto the resolved (already region-correct) sibling: %s", productID)
			}
			repointed = append(repointed, entryID)
			return nil
		},
	}
	enrich := &stubEnrichment{
		getProduct: func(_ context.Context, _ string, id uuid.UUID) (enrichapi.Product, error) {
			mu.Lock()
			getProductCalls[id]++
			mu.Unlock()
			switch id {
			case productManualJP:
				return manualJP, nil
			case productUnmatched:
				return unmatched, nil
			default:
				t.Fatalf("unexpected product id %s", id)
				return enrichapi.Product{}, nil
			}
		},
		resolve: func(_ context.Context, _ string, req enrichapi.ResolveRequest) (enrichapi.Product, error) {
			mu.Lock()
			resolveCalls++
			mu.Unlock()
			return manualJP, nil // the region-correct sibling entry1 already sits on
		},
	}
	srv, a := newUnitServer(t, st, enrich, newStubCache())
	triggerRematch(t, srv, a.token(t, uuid.NewString(), "admin"))

	reqtest.WaitFor(t, 5*time.Second, func() bool {
		mu.Lock()
		defer mu.Unlock()
		return len(repointed) == 1
	})

	mu.Lock()
	defer mu.Unlock()
	if resolveCalls != 1 {
		t.Fatalf("resolve must be called once for the whole triple, not per entry: %d", resolveCalls)
	}
	if len(repointed) != 1 || repointed[0] != entry2 {
		t.Fatalf("only the unmatched neighbor may repoint; the class-compatible entry must survive untouched: %v", repointed)
	}
	if getProductCalls[productManualJP] == 0 || getProductCalls[productUnmatched] == 0 {
		t.Fatalf("the per-entry guard needs both members fetched: %v", getProductCalls)
	}
}

// Cross-triple memoization: two entries seeded on the SAME member but
// grouped into different (game, platform, region) triples (two
// regions here) still trigger only one member fetch for that product
// across the whole run - the resnapshot lever pins its own dedup the
// same way (TestUnitInternalResnapshot_HappyPath's productCalls check).
func TestInternalRematchEntries_MemberFetchMemoizedAcrossTriples(t *testing.T) {
	productShared := uuid.New() // both triples start here: base class, region-correct for neither
	productJP, productPAL := uuid.New(), uuid.New()
	entryJP, entryPAL := uuid.New(), uuid.New()

	sharedMember := pricedGameProduct(productShared, "Super Nintendo") // base class
	jpMember := pricedGameProduct(productJP, "Super Famicom")
	palMember := pricedGameProduct(productPAL, "PAL Super Nintendo")

	refs := []store.RematchEntryRef{
		{EntryID: entryJP, ProductID: productShared, IGDBGameID: 1000, PlatformIGDBID: 6, Region: "ntsc_j"},
		{EntryID: entryPAL, ProductID: productShared, IGDBGameID: 1000, PlatformIGDBID: 6, Region: "pal"},
	}
	var mu sync.Mutex
	var repointed []uuid.UUID
	getProductCalls := map[uuid.UUID]int{}
	st := &stubStore{
		listAutoGameRematchRefs: func(context.Context) ([]store.RematchEntryRef, error) { return refs, nil },
		repointEntry: func(_ context.Context, entryID, _ uuid.UUID, _ *time.Time, _, _, _ *string, _, _ []string) error {
			mu.Lock()
			defer mu.Unlock()
			repointed = append(repointed, entryID)
			return nil
		},
	}
	enrich := &stubEnrichment{
		getProduct: func(_ context.Context, _ string, id uuid.UUID) (enrichapi.Product, error) {
			mu.Lock()
			getProductCalls[id]++
			mu.Unlock()
			if id != productShared {
				t.Fatalf("unexpected product id %s", id)
			}
			return sharedMember, nil
		},
		resolve: func(_ context.Context, _ string, req enrichapi.ResolveRequest) (enrichapi.Product, error) {
			if req.Region != nil && *req.Region == "pal" {
				return palMember, nil
			}
			return jpMember, nil
		},
	}
	srv, a := newUnitServer(t, st, enrich, newStubCache())
	triggerRematch(t, srv, a.token(t, uuid.NewString(), "admin"))

	reqtest.WaitFor(t, 5*time.Second, func() bool {
		mu.Lock()
		defer mu.Unlock()
		return len(repointed) == 2
	})

	mu.Lock()
	defer mu.Unlock()
	if len(getProductCalls) != 1 || getProductCalls[productShared] != 1 {
		t.Fatalf("GetProduct must be called exactly once for the shared member across both triples: %v", getProductCalls)
	}
}

// A failed member fetch or resolve counts the triple failed and the
// run continues to the next triple; the audit trail is the repoint
// log below (TestUnitRematchMetrics separately pins the triples.ok /
// triples.failed counters this scenario would also feed).
func TestInternalRematchEntries_CountsFailuresAndContinues(t *testing.T) {
	entryA, entryB, entryC := uuid.New(), uuid.New(), uuid.New()
	productA := uuid.New()                             // triple A: member fetch fails
	productB := uuid.New()                             // triple B: member fetch ok, resolve fails
	productCFrom, productCTo := uuid.New(), uuid.New() // triple C: healthy path

	refs := []store.RematchEntryRef{
		{EntryID: entryA, ProductID: productA, IGDBGameID: 1000, PlatformIGDBID: 6, Region: "ntsc_j"},
		{EntryID: entryB, ProductID: productB, IGDBGameID: 2000, PlatformIGDBID: 7, Region: "pal"},
		{EntryID: entryC, ProductID: productCFrom, IGDBGameID: 3000, PlatformIGDBID: 8, Region: "ntsc_j"},
	}
	var mu sync.Mutex
	var repointed []uuid.UUID
	st := &stubStore{
		listAutoGameRematchRefs: func(context.Context) ([]store.RematchEntryRef, error) { return refs, nil },
		repointEntry: func(_ context.Context, entryID, _ uuid.UUID, _ *time.Time, _, _, _ *string, _, _ []string) error {
			mu.Lock()
			defer mu.Unlock()
			repointed = append(repointed, entryID)
			return nil
		},
	}
	unmatchedB := gameProduct(productB)         // no pricecharting -> pending
	unmatchedCFrom := gameProduct(productCFrom) // no pricecharting -> pending
	resolvedCTo := pricedGameProduct(productCTo, "Super Famicom")
	enrich := &stubEnrichment{
		getProduct: func(_ context.Context, _ string, id uuid.UUID) (enrichapi.Product, error) {
			switch id {
			case productA:
				return enrichapi.Product{}, enrichmentclient.ErrUnavailable
			case productB:
				return unmatchedB, nil
			case productCFrom:
				return unmatchedCFrom, nil
			default:
				t.Fatalf("unexpected product id %s", id)
				return enrichapi.Product{}, nil
			}
		},
		resolve: func(_ context.Context, _ string, req enrichapi.ResolveRequest) (enrichapi.Product, error) {
			if req.Region != nil && *req.Region == "pal" {
				return enrichapi.Product{}, enrichmentclient.ErrUnavailable
			}
			return resolvedCTo, nil
		},
	}
	srv, a := newUnitServer(t, st, enrich, newStubCache())
	triggerRematch(t, srv, a.token(t, uuid.NewString(), "admin"))

	reqtest.WaitFor(t, 5*time.Second, func() bool {
		mu.Lock()
		defer mu.Unlock()
		return len(repointed) == 1
	})

	mu.Lock()
	defer mu.Unlock()
	if len(repointed) != 1 || repointed[0] != entryC {
		t.Fatalf("only triple C's entry may repoint; the two failed triples must leave their entries untouched: %v", repointed)
	}
}

// The rematch never lists user rows: a cross-class user entry in a
// swept triple is neither resolved nor repointed. ListAutoGameRematchRefs'
// match_provenance = 'auto' filter (TestListAutoGameRematchRefs) keeps
// it out before the handler ever sees it - the stub below models
// exactly that pre-filtered list, with only the auto entry present.
func TestInternalRematchEntries_SkipsUserPicks(t *testing.T) {
	productAuto := uuid.New() // entryAuto's member: unmatched -> needs repoint
	entryAuto := uuid.New()

	unmatchedAuto := gameProduct(productAuto) // no pricecharting -> never region-correct
	resolvedTo := pricedGameProduct(uuid.New(), "Super Famicom")

	refs := []store.RematchEntryRef{
		{EntryID: entryAuto, ProductID: productAuto, IGDBGameID: 1000, PlatformIGDBID: 6, Region: "ntsc_j"},
	}
	var mu sync.Mutex
	var repointed []uuid.UUID
	var resolveCalls int
	st := &stubStore{
		listAutoGameRematchRefs: func(context.Context) ([]store.RematchEntryRef, error) { return refs, nil },
		repointEntry: func(_ context.Context, entryID, _ uuid.UUID, _ *time.Time, _, _, _ *string, _, _ []string) error {
			if entryID != entryAuto {
				t.Fatalf("only the listed auto entry may repoint, got %s", entryID)
			}
			mu.Lock()
			repointed = append(repointed, entryID)
			mu.Unlock()
			return nil
		},
	}
	enrich := &stubEnrichment{
		getProduct: func(_ context.Context, _ string, id uuid.UUID) (enrichapi.Product, error) {
			if id != productAuto {
				t.Fatalf("unexpected product id %s", id)
			}
			return unmatchedAuto, nil
		},
		resolve: func(_ context.Context, _ string, req enrichapi.ResolveRequest) (enrichapi.Product, error) {
			mu.Lock()
			resolveCalls++
			mu.Unlock()
			return resolvedTo, nil
		},
	}
	srv, a := newUnitServer(t, st, enrich, newStubCache())
	triggerRematch(t, srv, a.token(t, uuid.NewString(), "admin"))

	reqtest.WaitFor(t, 5*time.Second, func() bool {
		mu.Lock()
		defer mu.Unlock()
		return len(repointed) == 1
	})

	mu.Lock()
	defer mu.Unlock()
	if resolveCalls != 1 {
		t.Fatalf("resolve calls: %d, want 1", resolveCalls)
	}
	if len(repointed) != 1 || repointed[0] != entryAuto {
		t.Fatalf("only the auto entry may repoint: %v", repointed)
	}
}

// A non-admin bearer on the entry rematch is forbidden; an all-nil
// stub store/enrichment panics if the handler ever reaches past the
// role check, so a silent bypass fails loudly rather than passing.
func TestInternalRematchEntries_NonAdminIsForbidden(t *testing.T) {
	srv, a := newUnitServer(t, &stubStore{}, &stubEnrichment{}, newStubCache())
	resp := do(t, http.MethodPost, srv.URL+"/internal/rematch-entries", a.token(t, uuid.NewString()), nil)
	wantProblem(t, resp, http.StatusForbidden, "forbidden")
}

// A service token passes the admin-or-service guard exactly like an
// admin bearer does - the CronJob's own credential.
func TestInternalRematchEntries_ServiceTokenIsAccepted(t *testing.T) {
	st := &stubStore{listAutoGameRematchRefs: func(context.Context) ([]store.RematchEntryRef, error) { return nil, nil }}
	srv, a := newUnitServer(t, st, &stubEnrichment{}, newStubCache())
	triggerRematch(t, srv, a.serviceToken(t, "svc:entry-rematch"))
}

// A concurrent trigger while a run is in flight answers 409
// rematch_in_progress instead of racing it (mirrors the catalog
// refresh's single-flight guard): the CAS happens synchronously at
// the trigger itself, so the first request answers 202 immediately
// and the second is refused while the detached sweep is still inside
// listAutoGameRematchRefs.
func TestInternalRematchEntries_ConcurrentTriggerIs409(t *testing.T) {
	release := make(chan struct{})
	started := make(chan struct{})
	st := &stubStore{
		listAutoGameRematchRefs: func(context.Context) ([]store.RematchEntryRef, error) {
			close(started)
			<-release
			return nil, nil
		},
	}
	srv, a := newUnitServer(t, st, &stubEnrichment{}, newStubCache())
	tok := a.token(t, uuid.NewString(), "admin")

	triggerRematch(t, srv, tok)
	<-started

	resp := do(t, http.MethodPost, srv.URL+"/internal/rematch-entries", tok, nil)
	wantProblem(t, resp, http.StatusConflict, "rematch_in_progress")
	close(release)

	// The first run's goroutine is unblocked now, but a black-box test
	// has no direct read on the in-process guard (the catalog
	// refresh's own detached tests poll one, unexported and
	// same-package there); retriggering until one is accepted again is
	// the external equivalent - each 409 in between means the first
	// run has not reset the guard yet. Swap the stub first: the first
	// run already consumed its one call to the blocking closure, and a
	// second call to it would close(started) again and panic.
	st.listAutoGameRematchRefs = func(context.Context) ([]store.RematchEntryRef, error) { return nil, nil }
	reqtest.WaitFor(t, 5*time.Second, func() bool {
		resp := do(t, http.MethodPost, srv.URL+"/internal/rematch-entries", tok, nil)
		return resp.StatusCode == http.StatusAccepted
	})
}

func TestCountProductReferences_AdminGateAndCount(t *testing.T) {
	var counted *uuid.UUID
	st := &stubStore{countEntriesByProduct: func(_ context.Context, productID uuid.UUID) (int64, error) {
		counted = &productID
		return 3, nil
	}}
	srv, a := newUnitServer(t, st, &stubEnrichment{}, newStubCache())
	pid := uuid.New()
	url := srv.URL + "/admin/products/" + pid.String() + "/references"

	// Non-admin: 403 with the forbidden code, count never served.
	resp := do(t, http.MethodGet, url, a.token(t, uuid.NewString()), nil)
	body, _ := io.ReadAll(resp.Body)
	_ = resp.Body.Close()
	if resp.StatusCode != http.StatusForbidden || !bytes.Contains(body, []byte("forbidden")) {
		t.Fatalf("non-admin: %d %s", resp.StatusCode, body)
	}
	if counted != nil {
		t.Fatal("the count must not run for a non-admin")
	}

	// Admin: the cross-user count for exactly the asked product.
	resp = do(t, http.MethodGet, url, a.token(t, uuid.NewString(), "admin"), nil)
	body, _ = io.ReadAll(resp.Body)
	_ = resp.Body.Close()
	if resp.StatusCode != http.StatusOK || !bytes.Contains(body, []byte(`"entry_count":3`)) {
		t.Fatalf("admin count: %d %s", resp.StatusCode, body)
	}
	if counted == nil || *counted != pid {
		t.Fatalf("counted product = %v, want %s", counted, pid)
	}
}

func TestNormalizePlatforms_MatchesAliasesSkipsUnknownAdminOnly(t *testing.T) {
	adminID := uuid.New()
	e1, e2, e3 := uuid.New(), uuid.New(), uuid.New()
	var stamped []uuid.UUID
	st := &stubStore{
		listNameOnlyPlatformEntries: func(context.Context) ([]store.PlatformEntryRef, error) {
			return []store.PlatformEntryRef{
				{EntryID: e1, PlatformName: "  SNES "},                             // alias match
				{EntryID: e2, PlatformName: "super nintendo entertainment system"}, // exact match
				{EntryID: e3, PlatformName: "my homebrew rig"},                     // no match
			}, nil
		},
		setEntryPlatformIdentity: func(_ context.Context, id uuid.UUID, igdbID int64, name string) error {
			if igdbID != 19 || name != "Super Nintendo Entertainment System" {
				t.Fatalf("stamp wrong: %d %q", igdbID, name)
			}
			stamped = append(stamped, id)
			return nil
		},
	}
	enr := &stubEnrichment{
		listPlatforms: func(context.Context, string) ([]enrichmentclient.Platform, error) {
			return []enrichmentclient.Platform{{IGDBID: 19, Name: "Super Nintendo Entertainment System", Aliases: []string{"snes", "super nintendo"}}}, nil
		},
	}
	srv, a := newUnitServer(t, st, enr, newStubCache())

	// Non-admin is 403; the store/enrichment never run.
	user := a.token(t, uuid.NewString())
	resp := do(t, http.MethodPost, srv.URL+"/internal/normalize-platforms", user, nil)
	wantProblem(t, resp, http.StatusForbidden, "forbidden")

	admin := a.token(t, adminID.String(), "admin")
	resp = do(t, http.MethodPost, srv.URL+"/internal/normalize-platforms", admin, nil)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("normalize: %d", resp.StatusCode)
	}
	var counts map[string]int
	if err := json.NewDecoder(resp.Body).Decode(&counts); err != nil {
		t.Fatal(err)
	}
	if counts["scanned"] != 3 || counts["normalized"] != 2 || counts["skipped"] != 1 {
		t.Fatalf("counts = %+v, want scanned 3 normalized 2 skipped 1", counts)
	}
	if len(stamped) != 2 {
		t.Fatalf("stamped %d entries, want 2", len(stamped))
	}
}

// TestNormalizePlatforms_ServiceToken pins the relaxed guard: a
// service token (the nightly job's own credential, same as
// normalize-regions and the entry rematch) now passes normalize-
// platforms end to end rather than being turned away. Both
// collaborators are wired with real (non-nil, no-op) stubs rather than
// left nil, since a service token reaching a nil stub would panic
// before this test could tell "reached the handler" apart from
// "the guard let it through and it actually ran".
func TestNormalizePlatforms_ServiceToken(t *testing.T) {
	st := &stubStore{listNameOnlyPlatformEntries: func(context.Context) ([]store.PlatformEntryRef, error) { return nil, nil }}
	enr := &stubEnrichment{listPlatforms: func(context.Context, string) ([]enrichmentclient.Platform, error) { return nil, nil }}
	srv, a := newUnitServer(t, st, enr, newStubCache())
	resp := do(t, http.MethodPost, srv.URL+"/internal/normalize-platforms", a.serviceToken(t, "svc:entry-rematch"), nil)
	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		t.Fatalf("service token: status %d, want 200: %s", resp.StatusCode, body)
	}
}

// TestNormalizePlatforms_UpstreamFailures pins that an unreachable
// enrichment or a failed store list surfaces as a problem+json error,
// rather than silently reporting 0 matches: an admin trusting the
// {scanned,normalized,skipped} counts must see 502/500, not a
// misleadingly clean "nothing matched" 200.
func TestNormalizePlatforms_UpstreamFailures(t *testing.T) {
	adminID := uuid.New()

	t.Run("enrichment down is 502", func(t *testing.T) {
		enr := &stubEnrichment{
			listPlatforms: func(context.Context, string) ([]enrichmentclient.Platform, error) {
				return nil, errors.New("enrichment down")
			},
		}
		srv, a := newUnitServer(t, &stubStore{}, enr, newStubCache())
		resp := do(t, http.MethodPost, srv.URL+"/internal/normalize-platforms", a.token(t, adminID.String(), "admin"), nil)
		wantProblem(t, resp, http.StatusBadGateway, "enrichment_unavailable")
	})

	t.Run("store list failure is 500", func(t *testing.T) {
		st := &stubStore{
			listNameOnlyPlatformEntries: func(context.Context) ([]store.PlatformEntryRef, error) {
				return nil, errors.New("db down")
			},
		}
		enr := &stubEnrichment{
			listPlatforms: func(context.Context, string) ([]enrichmentclient.Platform, error) {
				return []enrichmentclient.Platform{{IGDBID: 19, Name: "Super Nintendo Entertainment System"}}, nil
			},
		}
		srv, a := newUnitServer(t, st, enr, newStubCache())
		resp := do(t, http.MethodPost, srv.URL+"/internal/normalize-platforms", a.token(t, adminID.String(), "admin"), nil)
		wantProblem(t, resp, http.StatusInternalServerError, "internal")
	})
}

// ---- InternalNormalizeRegions ----

// TestNormalizeRegions_PromotesAndRepicks pins the fold+synonym
// promotion's two write arms: a custom entry (no product) gets a
// plain region write, a free-text region with no reviewed synonym is
// left untouched, and an igdb-backed entry additionally re-picks its
// localized snapshot for the promoted region from a freshly fetched
// product - the same GetProduct hop the region-edit arm of UpdateEntry
// uses. A chainless region (korea: no localization or release-date
// chain) promotes through the same snapshot arm with every localized
// field empty - the region graduates, its localization does not.
func TestNormalizeRegions_PromotesAndRepicks(t *testing.T) {
	adminID := uuid.New()
	custom := uuid.New()       // region "Japan", no product -> plain write
	brazilCustom := uuid.New() // region " BR ", no product -> synonym fold, plain write
	noSynonym := uuid.New()    // region " TAIWAN ", igdb-backed -> no fold match, stays
	igdbBacked := uuid.New()   // region "japan", igdb-backed -> fetch + snapshot re-pick
	koreaBacked := uuid.New()  // region " KOREA ", igdb-backed -> identity fold, chainless re-pick

	fetchedProduct := uuid.New() // the only product a matched row may fetch
	neverFetched := uuid.New()   // the no-synonym row's product; fetching it is a bug
	gameID := int64(1000)
	product := localizedGameProduct(fetchedProduct)

	refs := []store.OpenRegionEntryRef{
		{EntryID: custom, Region: "Japan"},
		{EntryID: brazilCustom, Region: " BR "},
		{EntryID: noSynonym, ProductID: &neverFetched, IGDBGameID: &gameID, Region: " TAIWAN "},
		{EntryID: igdbBacked, ProductID: &fetchedProduct, IGDBGameID: &gameID, Region: "japan"},
		{EntryID: koreaBacked, ProductID: &fetchedProduct, IGDBGameID: &gameID, Region: " KOREA "},
	}

	var mu sync.Mutex
	plainWrites := map[uuid.UUID]string{}
	type snapshotWrite struct {
		region                string
		name, translit, cover *string
	}
	snapshotWrites := map[uuid.UUID]snapshotWrite{}

	st := &stubStore{
		listOpenRegionEntries: func(context.Context, []string) ([]store.OpenRegionEntryRef, error) {
			return refs, nil
		},
		promoteEntryRegion: func(_ context.Context, entryID uuid.UUID, region string) error {
			mu.Lock()
			defer mu.Unlock()
			plainWrites[entryID] = region
			return nil
		},
		promoteEntryRegionSnapshot: func(_ context.Context, entryID uuid.UUID, region string, _ *time.Time, name, translit, cover *string) error {
			mu.Lock()
			defer mu.Unlock()
			snapshotWrites[entryID] = snapshotWrite{region, name, translit, cover}
			return nil
		},
	}
	enr := &stubEnrichment{
		getProduct: func(_ context.Context, _ string, id uuid.UUID) (enrichapi.Product, error) {
			if id != fetchedProduct {
				t.Fatalf("unexpected product fetch for %s (an unmatched row must never fetch)", id)
			}
			return product, nil
		},
	}
	srv, a := newUnitServer(t, st, enr, newStubCache())
	admin := a.token(t, adminID.String(), "admin")

	resp := do(t, http.MethodPost, srv.URL+"/internal/normalize-regions", admin, nil)
	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		t.Fatalf("status %d: %s", resp.StatusCode, body)
	}
	var counts map[string]int
	if err := json.NewDecoder(resp.Body).Decode(&counts); err != nil {
		t.Fatal(err)
	}
	if counts["scanned"] != 5 || counts["normalized"] != 4 || counts["skipped"] != 1 {
		t.Fatalf("counts = %+v, want scanned 5 normalized 4 skipped 1", counts)
	}

	mu.Lock()
	defer mu.Unlock()
	if got := plainWrites[custom]; got != "ntsc_j" {
		t.Fatalf("custom entry region = %q, want ntsc_j (plain write, no localized fields)", got)
	}
	if got := plainWrites[brazilCustom]; got != "brazil" {
		t.Fatalf("brazil-synonym entry region = %q, want brazil (the br row, trimmed and folded)", got)
	}
	if _, wrote := plainWrites[noSynonym]; wrote {
		t.Fatalf("no-synonym region must not be promoted")
	}
	if _, wrote := snapshotWrites[noSynonym]; wrote {
		t.Fatalf("no-synonym region must not be promoted")
	}
	got, ok := snapshotWrites[igdbBacked]
	if !ok {
		t.Fatalf("igdb-backed entry must re-pick its snapshot, not take the plain-write arm")
	}
	if got.region != "ntsc_j" {
		t.Fatalf("igdb-backed region = %q, want ntsc_j", got.region)
	}
	// wantJP is localizedGameProduct's ja-JP bundle (index 0); comparing
	// against it directly, rather than a copied literal, keeps this
	// assertion honest if that fixture ever changes.
	wantJP := (*product.Igdb.Localizations)[0]
	if got.name == nil || wantJP.Name == nil || *got.name != *wantJP.Name {
		t.Fatalf("localized_name must come from the fetched product's ja-JP bundle: got %v", got.name)
	}

	kr, ok := snapshotWrites[koreaBacked]
	if !ok {
		t.Fatalf("korea igdb-backed entry must promote through the snapshot arm")
	}
	if kr.region != "korea" {
		t.Fatalf("korea-backed region = %q, want korea (identity fold)", kr.region)
	}
	// korea's chain reads the ko-KR bundle; the fixture row is
	// name-only, so translit and cover stay empty on the re-pick.
	wantKO := (*product.Igdb.Localizations)[2]
	if kr.name == nil || wantKO.Name == nil || *kr.name != *wantKO.Name {
		t.Fatalf("localized_name must come from the fetched product's ko-KR bundle: got %v", kr.name)
	}
	if kr.translit != nil || kr.cover != nil {
		t.Fatalf("sparse ko-KR bundle must leave translit and cover empty, got %+v", kr)
	}
}

// TestNormalizeRegions_FetchFailureSkips pins the per-row skip: an
// enrichment outage on one igdb-backed row's product fetch counts
// against skipped and leaves the row untouched rather than failing the
// whole sweep - this lever has no whole-run 502 (it has no platform-
// catalog dependency the way normalize-platforms does).
func TestNormalizeRegions_FetchFailureSkips(t *testing.T) {
	adminID := uuid.New()
	entry := uuid.New()
	productID := uuid.New()
	gameID := int64(2000)

	refs := []store.OpenRegionEntryRef{
		{EntryID: entry, ProductID: &productID, IGDBGameID: &gameID, Region: "japan"},
	}
	st := &stubStore{
		listOpenRegionEntries: func(context.Context, []string) ([]store.OpenRegionEntryRef, error) {
			return refs, nil
		},
		// promoteEntryRegion and promoteEntryRegionSnapshot are
		// deliberately left nil: either being called after a fetch
		// failure is a bug, and the stub's nil panic catches it loudly.
	}
	enr := &stubEnrichment{
		getProduct: func(context.Context, string, uuid.UUID) (enrichapi.Product, error) {
			return enrichapi.Product{}, enrichmentclient.ErrUnavailable
		},
	}
	srv, a := newUnitServer(t, st, enr, newStubCache())
	admin := a.token(t, adminID.String(), "admin")

	resp := do(t, http.MethodPost, srv.URL+"/internal/normalize-regions", admin, nil)
	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		t.Fatalf("status %d: %s", resp.StatusCode, body)
	}
	var counts map[string]int
	if err := json.NewDecoder(resp.Body).Decode(&counts); err != nil {
		t.Fatal(err)
	}
	if counts["scanned"] != 1 || counts["normalized"] != 0 || counts["skipped"] != 1 {
		t.Fatalf("counts = %+v, want scanned 1 normalized 0 skipped 1", counts)
	}
}

// TestNormalizeRegions_Guards mirrors the entry rematch's admin-or-
// service guard tests: a service token (the nightly job's own
// credential) passes, a plain user token is forbidden.
func TestNormalizeRegions_Guards(t *testing.T) {
	st := &stubStore{listOpenRegionEntries: func(context.Context, []string) ([]store.OpenRegionEntryRef, error) { return nil, nil }}
	srv, a := newUnitServer(t, st, &stubEnrichment{}, newStubCache())

	resp := do(t, http.MethodPost, srv.URL+"/internal/normalize-regions", a.serviceToken(t, "svc:normalize-regions"), nil)
	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		t.Fatalf("service token: status %d, want 200: %s", resp.StatusCode, body)
	}

	resp = do(t, http.MethodPost, srv.URL+"/internal/normalize-regions", a.token(t, uuid.NewString()), nil)
	wantProblem(t, resp, http.StatusForbidden, "forbidden")
}
