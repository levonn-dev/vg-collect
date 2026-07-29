package server_test

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	openapi_types "github.com/oapi-codegen/runtime/types"
	"github.com/testcontainers/testcontainers-go"
	tcpostgres "github.com/testcontainers/testcontainers-go/modules/postgres"
	tcvalkey "github.com/testcontainers/testcontainers-go/modules/valkey"
	"github.com/testcontainers/testcontainers-go/wait"

	"github.com/levonn-dev/vgkeep/libs/go/pgkit"
	"github.com/levonn-dev/vgkeep/libs/go/valkeykit"
	"github.com/levonn-dev/vgkeep/services/collection/internal/cache"
	"github.com/levonn-dev/vgkeep/services/collection/internal/enrichmentclient"
	"github.com/levonn-dev/vgkeep/services/collection/internal/gen/api"
	"github.com/levonn-dev/vgkeep/services/collection/internal/gen/enrichapi"
	"github.com/levonn-dev/vgkeep/services/collection/internal/server"
	"github.com/levonn-dev/vgkeep/services/collection/internal/store"
	"github.com/levonn-dev/vgkeep/services/collection/migrations"
)

// ---- stub doubles (function fields; a nil field panics loudly) ----

// stubStore implements server.Store via function fields.
type stubStore struct {
	createEntry       func(ctx context.Context, e store.Entry, tagIDs []uuid.UUID) (store.Entry, error)
	getEntry          func(ctx context.Context, userID, id uuid.UUID) (store.Entry, error)
	updateEntry       func(ctx context.Context, e store.Entry, tagIDs []uuid.UUID) (store.Entry, error)
	deleteEntry       func(ctx context.Context, userID, id uuid.UUID) error
	bulkUpdateEntries func(ctx context.Context, userID uuid.UUID, entryIDs []uuid.UUID, actions store.BulkActions) (int, error)
	reorder           func(ctx context.Context, userID, entryID uuid.UUID, afterID, beforeID *uuid.UUID) (store.Entry, error)
	listEntries       func(ctx context.Context, userID uuid.UUID, f store.Filters) ([]store.Entry, error)
	librarySummary    func(ctx context.Context, userID uuid.UUID) ([]store.LibraryGame, error)
	listTags          func(ctx context.Context, userID uuid.UUID) ([]store.Tag, error)
	createTag         func(ctx context.Context, userID uuid.UUID, name string) (store.Tag, error)
	renameTag         func(ctx context.Context, userID, id uuid.UUID, name string) (store.Tag, error)
	deleteTag         func(ctx context.Context, userID, id uuid.UUID) error
	listViews         func(ctx context.Context, userID uuid.UUID) ([]store.View, error)
	createView        func(ctx context.Context, userID uuid.UUID, name string, params []byte, visibility string) (store.View, error)
	updateView        func(ctx context.Context, userID, id uuid.UUID, name string, params []byte, visibility string) (store.View, error)
	deleteView        func(ctx context.Context, userID, id uuid.UUID) error
	seedDefaultViews  func(ctx context.Context, userID uuid.UUID) error
	dashboardCounts   func(ctx context.Context, userID uuid.UUID, f store.Filters) (store.DashboardCounts, error)
	pricingRows       func(ctx context.Context, userID uuid.UUID, f store.Filters) ([]store.PricingRow, error)
	purgeUserData     func(ctx context.Context, userID uuid.UUID) error

	getSharedShelf       func(ctx context.Context, id uuid.UUID) (store.View, error)
	getSharedShelfBySlug func(ctx context.Context, ownerID uuid.UUID, foldedSlug string) (store.View, error)
	listListedShelves    func(ctx context.Context, ownerIDs []uuid.UUID, limit, offset int) ([]store.View, int, error)
	sharedShelvesByIDs   func(ctx context.Context, ids []uuid.UUID) ([]store.View, error)
	countEntriesFiltered func(ctx context.Context, userID uuid.UUID, f store.Filters) (int, error)
	coverURLs            func(ctx context.Context, userID uuid.UUID, f store.Filters, limit int) ([]string, error)

	listGameBackedRefs    func(ctx context.Context) ([]store.GameEntryRef, error)
	setFirstReleaseDate   func(ctx context.Context, entryID uuid.UUID, d *time.Time) error
	countEntriesByProduct func(ctx context.Context, productID uuid.UUID) (int64, error)

	listNameOnlyPlatformEntries func(ctx context.Context) ([]store.PlatformEntryRef, error)
	setEntryPlatformIdentity    func(ctx context.Context, entryID uuid.UUID, igdbID int64, name string) error

	createSubmission                 func(ctx context.Context, userID, entryID uuid.UUID) (store.Submission, error)
	latestSubmissionForEntry         func(ctx context.Context, userID, entryID uuid.UUID) (store.Submission, error)
	latestApprovedSubmissionForEntry func(ctx context.Context, userID, entryID uuid.UUID) (store.Submission, error)
	ackSubmissionResolution          func(ctx context.Context, id uuid.UUID) error
	cancelSubmission                 func(ctx context.Context, userID, entryID uuid.UUID) error
	getSubmission                    func(ctx context.Context, id uuid.UUID) (store.Submission, error)
	countPendingSubmissions          func(ctx context.Context, userID uuid.UUID) (int64, error)
	countAllPendingSubmissions       func(ctx context.Context) (int64, error)
	countSubmissionsSince            func(ctx context.Context, userID uuid.UUID, since time.Time) (int64, error)
	listPendingSubmissions           func(ctx context.Context, limit, offset int) ([]store.SubmissionProposal, int64, error)
	rejectSubmission                 func(ctx context.Context, id uuid.UUID, reason string) (store.Submission, error)
	recordSubmissionProduct          func(ctx context.Context, id, productID uuid.UUID) error
	approveSubmission                func(ctx context.Context, id uuid.UUID, snap store.CatalogSnapshot) (store.Submission, error)
}

var _ server.Store = (*stubStore)(nil)

func (s *stubStore) CreateEntry(ctx context.Context, e store.Entry, tagIDs []uuid.UUID) (store.Entry, error) {
	if s.createEntry == nil {
		panic("unexpected CreateEntry")
	}
	return s.createEntry(ctx, e, tagIDs)
}
func (s *stubStore) GetEntry(ctx context.Context, userID, id uuid.UUID) (store.Entry, error) {
	if s.getEntry == nil {
		panic("unexpected GetEntry")
	}
	return s.getEntry(ctx, userID, id)
}
func (s *stubStore) UpdateEntry(ctx context.Context, e store.Entry, tagIDs []uuid.UUID) (store.Entry, error) {
	if s.updateEntry == nil {
		panic("unexpected UpdateEntry")
	}
	return s.updateEntry(ctx, e, tagIDs)
}
func (s *stubStore) DeleteEntry(ctx context.Context, userID, id uuid.UUID) error {
	if s.deleteEntry == nil {
		panic("unexpected DeleteEntry")
	}
	return s.deleteEntry(ctx, userID, id)
}
func (s *stubStore) BulkUpdateEntries(ctx context.Context, userID uuid.UUID, entryIDs []uuid.UUID, actions store.BulkActions) (int, error) {
	if s.bulkUpdateEntries == nil {
		panic("unexpected BulkUpdateEntries")
	}
	return s.bulkUpdateEntries(ctx, userID, entryIDs, actions)
}
func (s *stubStore) Reorder(ctx context.Context, userID, entryID uuid.UUID, afterID, beforeID *uuid.UUID) (store.Entry, error) {
	if s.reorder == nil {
		panic("unexpected Reorder")
	}
	return s.reorder(ctx, userID, entryID, afterID, beforeID)
}
func (s *stubStore) ListEntries(ctx context.Context, userID uuid.UUID, f store.Filters) ([]store.Entry, error) {
	if s.listEntries == nil {
		panic("unexpected ListEntries")
	}
	return s.listEntries(ctx, userID, f)
}
func (s *stubStore) LibrarySummary(ctx context.Context, userID uuid.UUID) ([]store.LibraryGame, error) {
	if s.librarySummary == nil {
		panic("unexpected LibrarySummary")
	}
	return s.librarySummary(ctx, userID)
}
func (s *stubStore) ListTags(ctx context.Context, userID uuid.UUID) ([]store.Tag, error) {
	if s.listTags == nil {
		panic("unexpected ListTags")
	}
	return s.listTags(ctx, userID)
}
func (s *stubStore) CreateTag(ctx context.Context, userID uuid.UUID, name string) (store.Tag, error) {
	if s.createTag == nil {
		panic("unexpected CreateTag")
	}
	return s.createTag(ctx, userID, name)
}
func (s *stubStore) RenameTag(ctx context.Context, userID, id uuid.UUID, name string) (store.Tag, error) {
	if s.renameTag == nil {
		panic("unexpected RenameTag")
	}
	return s.renameTag(ctx, userID, id, name)
}
func (s *stubStore) DeleteTag(ctx context.Context, userID, id uuid.UUID) error {
	if s.deleteTag == nil {
		panic("unexpected DeleteTag")
	}
	return s.deleteTag(ctx, userID, id)
}
func (s *stubStore) ListViews(ctx context.Context, userID uuid.UUID) ([]store.View, error) {
	if s.listViews == nil {
		panic("unexpected ListViews")
	}
	return s.listViews(ctx, userID)
}
func (s *stubStore) CreateView(ctx context.Context, userID uuid.UUID, name string, params []byte, visibility string) (store.View, error) {
	if s.createView == nil {
		panic("unexpected CreateView")
	}
	return s.createView(ctx, userID, name, params, visibility)
}
func (s *stubStore) UpdateView(ctx context.Context, userID, id uuid.UUID, name string, params []byte, visibility string) (store.View, error) {
	if s.updateView == nil {
		panic("unexpected UpdateView")
	}
	return s.updateView(ctx, userID, id, name, params, visibility)
}
func (s *stubStore) DeleteView(ctx context.Context, userID, id uuid.UUID) error {
	if s.deleteView == nil {
		panic("unexpected DeleteView")
	}
	return s.deleteView(ctx, userID, id)
}
func (s *stubStore) SeedDefaultViews(ctx context.Context, userID uuid.UUID) error {
	if s.seedDefaultViews == nil {
		panic("unexpected SeedDefaultViews")
	}
	return s.seedDefaultViews(ctx, userID)
}
func (s *stubStore) GetSharedShelf(ctx context.Context, id uuid.UUID) (store.View, error) {
	if s.getSharedShelf == nil {
		panic("unexpected GetSharedShelf")
	}
	return s.getSharedShelf(ctx, id)
}
func (s *stubStore) GetSharedShelfBySlug(ctx context.Context, ownerID uuid.UUID, foldedSlug string) (store.View, error) {
	if s.getSharedShelfBySlug == nil {
		panic("unexpected GetSharedShelfBySlug")
	}
	return s.getSharedShelfBySlug(ctx, ownerID, foldedSlug)
}
func (s *stubStore) ListListedShelves(ctx context.Context, ownerIDs []uuid.UUID, limit, offset int) ([]store.View, int, error) {
	if s.listListedShelves == nil {
		panic("unexpected ListListedShelves")
	}
	return s.listListedShelves(ctx, ownerIDs, limit, offset)
}
func (s *stubStore) SharedShelvesByIDs(ctx context.Context, ids []uuid.UUID) ([]store.View, error) {
	if s.sharedShelvesByIDs == nil {
		panic("unexpected SharedShelvesByIDs")
	}
	return s.sharedShelvesByIDs(ctx, ids)
}
func (s *stubStore) CountEntriesFiltered(ctx context.Context, userID uuid.UUID, f store.Filters) (int, error) {
	if s.countEntriesFiltered == nil {
		panic("unexpected CountEntriesFiltered")
	}
	return s.countEntriesFiltered(ctx, userID, f)
}
func (s *stubStore) CoverURLs(ctx context.Context, userID uuid.UUID, f store.Filters, limit int) ([]string, error) {
	if s.coverURLs == nil {
		panic("unexpected CoverURLs")
	}
	return s.coverURLs(ctx, userID, f, limit)
}
func (s *stubStore) DashboardCounts(ctx context.Context, userID uuid.UUID, f store.Filters) (store.DashboardCounts, error) {
	if s.dashboardCounts == nil {
		panic("unexpected DashboardCounts")
	}
	return s.dashboardCounts(ctx, userID, f)
}
func (s *stubStore) PricingRows(ctx context.Context, userID uuid.UUID, f store.Filters) ([]store.PricingRow, error) {
	if s.pricingRows == nil {
		panic("unexpected PricingRows")
	}
	return s.pricingRows(ctx, userID, f)
}
func (s *stubStore) PurgeUserData(ctx context.Context, userID uuid.UUID) error {
	if s.purgeUserData == nil {
		panic("unexpected PurgeUserData")
	}
	return s.purgeUserData(ctx, userID)
}
func (s *stubStore) ListGameBackedRefs(ctx context.Context) ([]store.GameEntryRef, error) {
	if s.listGameBackedRefs == nil {
		panic("unexpected ListGameBackedRefs")
	}
	return s.listGameBackedRefs(ctx)
}
func (s *stubStore) CountEntriesByProduct(ctx context.Context, productID uuid.UUID) (int64, error) {
	if s.countEntriesByProduct == nil {
		panic("unexpected CountEntriesByProduct")
	}
	return s.countEntriesByProduct(ctx, productID)
}
func (s *stubStore) SetFirstReleaseDate(ctx context.Context, entryID uuid.UUID, d *time.Time) error {
	if s.setFirstReleaseDate == nil {
		panic("unexpected SetFirstReleaseDate")
	}
	return s.setFirstReleaseDate(ctx, entryID, d)
}
func (s *stubStore) ListNameOnlyPlatformEntries(ctx context.Context) ([]store.PlatformEntryRef, error) {
	if s.listNameOnlyPlatformEntries == nil {
		panic("unexpected ListNameOnlyPlatformEntries")
	}
	return s.listNameOnlyPlatformEntries(ctx)
}
func (s *stubStore) SetEntryPlatformIdentity(ctx context.Context, entryID uuid.UUID, igdbID int64, name string) error {
	if s.setEntryPlatformIdentity == nil {
		panic("unexpected SetEntryPlatformIdentity")
	}
	return s.setEntryPlatformIdentity(ctx, entryID, igdbID, name)
}
func (s *stubStore) CreateSubmission(ctx context.Context, userID, entryID uuid.UUID) (store.Submission, error) {
	if s.createSubmission == nil {
		panic("unexpected CreateSubmission")
	}
	return s.createSubmission(ctx, userID, entryID)
}
func (s *stubStore) LatestSubmissionForEntry(ctx context.Context, userID, entryID uuid.UUID) (store.Submission, error) {
	if s.latestSubmissionForEntry == nil {
		panic("unexpected LatestSubmissionForEntry")
	}
	return s.latestSubmissionForEntry(ctx, userID, entryID)
}
func (s *stubStore) LatestApprovedSubmissionForEntry(ctx context.Context, userID, entryID uuid.UUID) (store.Submission, error) {
	if s.latestApprovedSubmissionForEntry == nil {
		panic("unexpected LatestApprovedSubmissionForEntry")
	}
	return s.latestApprovedSubmissionForEntry(ctx, userID, entryID)
}
func (s *stubStore) AckSubmissionResolution(ctx context.Context, id uuid.UUID) error {
	if s.ackSubmissionResolution == nil {
		panic("unexpected AckSubmissionResolution")
	}
	return s.ackSubmissionResolution(ctx, id)
}
func (s *stubStore) CancelSubmission(ctx context.Context, userID, entryID uuid.UUID) error {
	if s.cancelSubmission == nil {
		panic("unexpected CancelSubmission")
	}
	return s.cancelSubmission(ctx, userID, entryID)
}
func (s *stubStore) GetSubmission(ctx context.Context, id uuid.UUID) (store.Submission, error) {
	if s.getSubmission == nil {
		panic("unexpected GetSubmission")
	}
	return s.getSubmission(ctx, id)
}
func (s *stubStore) CountPendingSubmissions(ctx context.Context, userID uuid.UUID) (int64, error) {
	if s.countPendingSubmissions == nil {
		panic("unexpected CountPendingSubmissions")
	}
	return s.countPendingSubmissions(ctx, userID)
}
func (s *stubStore) CountAllPendingSubmissions(ctx context.Context) (int64, error) {
	if s.countAllPendingSubmissions == nil {
		panic("unexpected CountAllPendingSubmissions")
	}
	return s.countAllPendingSubmissions(ctx)
}
func (s *stubStore) CountSubmissionsSince(ctx context.Context, userID uuid.UUID, since time.Time) (int64, error) {
	if s.countSubmissionsSince == nil {
		panic("unexpected CountSubmissionsSince")
	}
	return s.countSubmissionsSince(ctx, userID, since)
}
func (s *stubStore) ListPendingSubmissions(ctx context.Context, limit, offset int) ([]store.SubmissionProposal, int64, error) {
	if s.listPendingSubmissions == nil {
		panic("unexpected ListPendingSubmissions")
	}
	return s.listPendingSubmissions(ctx, limit, offset)
}
func (s *stubStore) RejectSubmission(ctx context.Context, id uuid.UUID, reason string) (store.Submission, error) {
	if s.rejectSubmission == nil {
		panic("unexpected RejectSubmission")
	}
	return s.rejectSubmission(ctx, id, reason)
}
func (s *stubStore) RecordSubmissionProduct(ctx context.Context, id, productID uuid.UUID) error {
	if s.recordSubmissionProduct == nil {
		panic("unexpected RecordSubmissionProduct")
	}
	return s.recordSubmissionProduct(ctx, id, productID)
}
func (s *stubStore) ApproveSubmission(ctx context.Context, id uuid.UUID, snap store.CatalogSnapshot) (store.Submission, error) {
	if s.approveSubmission == nil {
		panic("unexpected ApproveSubmission")
	}
	return s.approveSubmission(ctx, id, snap)
}

// stubEnrichment implements server.Enrichment via function fields.
type stubEnrichment struct {
	getProduct             func(ctx context.Context, bearer string, id uuid.UUID) (enrichapi.Product, error)
	batchPrices            func(ctx context.Context, bearer string, ids []uuid.UUID) (map[string]enrichapi.ProductPrices, error)
	priceHistory           func(ctx context.Context, bearer string, ids []uuid.UUID, days int) (map[string][]enrichapi.PricePoint, error)
	createCommunityProduct func(ctx context.Context, bearer string, req enrichapi.CreateCommunityProductJSONRequestBody) (enrichapi.Product, error)
	listPlatforms          func(ctx context.Context, bearer string) ([]enrichmentclient.Platform, error)
}

var _ server.Enrichment = (*stubEnrichment)(nil)

func (s *stubEnrichment) GetProduct(ctx context.Context, bearer string, id uuid.UUID) (enrichapi.Product, error) {
	if s.getProduct == nil {
		panic("unexpected GetProduct")
	}
	return s.getProduct(ctx, bearer, id)
}
func (s *stubEnrichment) BatchPrices(ctx context.Context, bearer string, ids []uuid.UUID) (map[string]enrichapi.ProductPrices, error) {
	if s.batchPrices == nil {
		panic("unexpected BatchPrices")
	}
	return s.batchPrices(ctx, bearer, ids)
}
func (s *stubEnrichment) PriceHistory(ctx context.Context, bearer string, ids []uuid.UUID, days int) (map[string][]enrichapi.PricePoint, error) {
	if s.priceHistory == nil {
		panic("unexpected PriceHistory")
	}
	return s.priceHistory(ctx, bearer, ids, days)
}
func (s *stubEnrichment) CreateCommunityProduct(ctx context.Context, bearer string, req enrichapi.CreateCommunityProductJSONRequestBody) (enrichapi.Product, error) {
	if s.createCommunityProduct == nil {
		panic("unexpected CreateCommunityProduct")
	}
	return s.createCommunityProduct(ctx, bearer, req)
}
func (s *stubEnrichment) ListPlatforms(ctx context.Context, bearer string) ([]enrichmentclient.Platform, error) {
	if s.listPlatforms == nil {
		panic("unexpected ListPlatforms")
	}
	return s.listPlatforms(ctx, bearer)
}

// stubCache implements server.Cache in memory, recording invalidations.
type stubCache struct {
	mu          sync.Mutex
	bodies      map[string][]byte
	vhBodies    map[string][]byte
	invalidated []string
	err         error // returned by every method when set
}

var _ server.Cache = (*stubCache)(nil)

func newStubCache() *stubCache {
	return &stubCache{bodies: map[string][]byte{}, vhBodies: map[string][]byte{}}
}

func (s *stubCache) GetDashboard(_ context.Context, sub string) ([]byte, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.err != nil {
		return nil, s.err
	}
	return s.bodies[sub], nil
}
func (s *stubCache) PutDashboard(_ context.Context, sub string, body []byte, _ time.Duration) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.err != nil {
		return s.err
	}
	s.bodies[sub] = body
	return nil
}
func (s *stubCache) GetValueHistory(_ context.Context, sub string) ([]byte, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.err != nil {
		return nil, s.err
	}
	return s.vhBodies[sub], nil
}
func (s *stubCache) PutValueHistory(_ context.Context, sub string, body []byte, _ time.Duration) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.err != nil {
		return s.err
	}
	s.vhBodies[sub] = body
	return nil
}
func (s *stubCache) InvalidateDashboard(_ context.Context, sub string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.invalidated = append(s.invalidated, sub)
	if s.err != nil {
		return s.err
	}
	delete(s.bodies, sub)
	delete(s.vhBodies, sub)
	return nil
}

// ---- fixtures ----

func ptr[T any](v T) *T { return &v }

func jsonBody(m map[string]any) *bytes.Reader {
	b, _ := json.Marshal(m)
	return bytes.NewReader(b)
}

// gameProduct is a canonical enrichment product for snapshot tests.
func gameProduct(id uuid.UUID) enrichapi.Product {
	released := openapi_types.Date{Time: time.Date(1995, time.March, 11, 0, 0, 0, 0, time.UTC)}
	return enrichapi.Product{
		Id:   id,
		Type: enrichapi.ProductType("game"),
		Name: "Chrono Trigger",
		Platform: &enrichapi.PlatformRef{
			IgdbPlatformId: 6, Name: "SNES",
		},
		Igdb: &enrichapi.IgdbMeta{
			GameId: 1000, Name: "Chrono Trigger", Genres: []string{"RPG"},
			Themes: []string{}, Franchises: []string{}, SimilarGames: []int64{},
			Companies: []enrichapi.CompanyCredit{}, FirstReleaseDate: &released,
		},
	}
}

// consoleProduct is a hardware fixture: a valid proxy pricing target
// that carries no IGDB game identity of its own.
func consoleProduct(id uuid.UUID) enrichapi.Product {
	return enrichapi.Product{
		Id:   id,
		Type: enrichapi.ProductType("console"),
		Name: "Super NES Console",
	}
}

// pricedAs answers BatchPrices with the same triple for every id.
func pricedAs(loose, cib, sealed int64) func(context.Context, string, []uuid.UUID) (map[string]enrichapi.ProductPrices, error) {
	return func(_ context.Context, _ string, ids []uuid.UUID) (map[string]enrichapi.ProductPrices, error) {
		out := map[string]enrichapi.ProductPrices{}
		for _, id := range ids {
			l, c, n := loose, cib, sealed
			out[id.String()] = enrichapi.ProductPrices{LooseCents: &l, CibCents: &c, NewCents: &n}
		}
		return out, nil
	}
}

// createBody is a minimal valid creation body for productID.
func createBody(productID uuid.UUID, mutate func(map[string]any)) *bytes.Reader {
	m := map[string]any{
		"product_id": productID.String(),
		"region":     "ntsc_u",
		"packaging":  "cib",
	}
	if mutate != nil {
		mutate(m)
	}
	b, _ := json.Marshal(m)
	return bytes.NewReader(b)
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
				Id: id, Type: enrichapi.ProductType("console"), Name: "Gamecube System",
				Platform: &enrichapi.PlatformRef{IgdbPlatformId: 21, Name: "Nintendo GameCube", LogoUrl: &logo},
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

func TestUnitCreateEntry_ValidationMatrix(t *testing.T) {
	productID := uuid.New()
	cases := []struct {
		name   string
		mutate func(map[string]any)
		code   string
	}{
		{"bad region", func(m map[string]any) { m["region"] = "ntsc" }, "invalid_body"},
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
		p.Igdb.ReleaseDates = &[]enrichapi.ReleaseDate{
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

// ---- integration stack (real Postgres + real Valkey + fake enrichment) ----

// stubEnrichmentService is the httptest twin of the enrichment
// service: contract-shaped answers for the two consumed endpoints,
// with a mutable product registry and a call counter.
type stubEnrichmentService struct {
	srv *httptest.Server

	mu        sync.Mutex
	products  map[uuid.UUID]enrichapi.Product
	prices    map[uuid.UUID]enrichapi.ProductPrices
	down      bool
	batchHits int
}

func newStubEnrichmentService(t *testing.T) *stubEnrichmentService {
	t.Helper()
	f := &stubEnrichmentService{
		products: map[uuid.UUID]enrichapi.Product{},
		prices:   map[uuid.UUID]enrichapi.ProductPrices{},
	}
	mux := http.NewServeMux()
	mux.HandleFunc("GET /products/{id}", func(w http.ResponseWriter, r *http.Request) {
		f.mu.Lock()
		defer f.mu.Unlock()
		if f.down {
			w.WriteHeader(http.StatusInternalServerError)
			return
		}
		id, err := uuid.Parse(r.PathValue("id"))
		if err != nil {
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		p, ok := f.products[id]
		if !ok {
			w.Header().Set("Content-Type", "application/problem+json")
			w.WriteHeader(http.StatusNotFound)
			_, _ = w.Write([]byte(`{"type":"about:blank","title":"Not Found","status":404,"code":"product_not_found"}`))
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(p)
	})
	mux.HandleFunc("POST /products/prices:batch", func(w http.ResponseWriter, r *http.Request) {
		f.mu.Lock()
		defer f.mu.Unlock()
		f.batchHits++
		if f.down {
			w.WriteHeader(http.StatusInternalServerError)
			return
		}
		var req struct {
			ProductIDs []uuid.UUID `json:"product_ids"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		prices := map[string]enrichapi.ProductPrices{}
		for _, id := range req.ProductIDs {
			if p, ok := f.prices[id]; ok {
				prices[id.String()] = p
			}
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{"prices": prices})
	})
	mux.HandleFunc("POST /products/price-history:batch", func(w http.ResponseWriter, r *http.Request) {
		f.mu.Lock()
		defer f.mu.Unlock()
		if f.down {
			w.WriteHeader(http.StatusInternalServerError)
			return
		}
		var req struct {
			ProductIDs []uuid.UUID `json:"product_ids"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		series := map[string]any{}
		for _, id := range req.ProductIDs {
			if p, ok := f.prices[id]; ok {
				series[id.String()] = []map[string]any{{
					"captured_at": "2026-07-01T06:00:00Z",
					"loose_cents": p.LooseCents, "cib_cents": p.CibCents, "new_cents": p.NewCents,
				}}
			}
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{"series": series})
	})
	f.srv = httptest.NewServer(mux)
	t.Cleanup(f.srv.Close)
	return f
}

// addGame registers a priced game product and returns its id.
func (f *stubEnrichmentService) addGame(name string, loose, cib, sealed int64) uuid.UUID {
	f.mu.Lock()
	defer f.mu.Unlock()
	id := uuid.New()
	p := gameProduct(id)
	p.Name = name
	f.products[id] = p
	f.prices[id] = enrichapi.ProductPrices{LooseCents: &loose, CibCents: &cib, NewCents: &sealed}
	return id
}

// stack is the full vertical: real Postgres store + real Valkey cache
// + the enrichment fake + the real router. Skips on -short.
type stack struct {
	baseURL string
	auth    authEnv
	store   *store.Store
	cache   *cache.Cache
	enrich  *stubEnrichmentService
}

func newStack(t *testing.T) *stack {
	t.Helper()
	if testing.Short() {
		t.Skip("requires docker")
	}
	ctx := context.Background()

	pg, err := tcpostgres.Run(ctx, "postgres:17-alpine",
		tcpostgres.WithDatabase("collection"), tcpostgres.WithUsername("c"), tcpostgres.WithPassword("p"),
		testcontainers.WithWaitStrategy(
			wait.ForLog("database system is ready to accept connections").
				WithOccurrence(2).WithStartupTimeout(60*time.Second)))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = pg.Terminate(ctx) })
	url, err := pg.ConnectionString(ctx, "sslmode=disable")
	if err != nil {
		t.Fatal(err)
	}
	if err := pgkit.Migrate(url, migrations.FS, "."); err != nil {
		t.Fatal(err)
	}
	pool, err := pgkit.Connect(ctx, url)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(pool.Close)

	vk, err := tcvalkey.Run(ctx, "valkey/valkey:8-alpine")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = vk.Terminate(ctx) })
	vkURL, err := vk.ConnectionString(ctx)
	if err != nil {
		t.Fatal(err)
	}
	rdb, err := valkeykit.Connect(ctx, vkURL)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = rdb.Close() })

	fe := newStubEnrichmentService(t)
	enrichClient, err := enrichmentclient.New(fe.srv.URL)
	if err != nil {
		t.Fatal(err)
	}

	a := newAuthEnv(t)
	st := store.New(pool)
	c := cache.New(rdb)
	h := server.New(st, enrichClient, c, server.Options{
		DashboardCacheTTL: 5 * time.Minute,
		Logger:            testLogger(),
	})
	router := server.NewRouter(h, a.v, testLogger(),
		func(ctx context.Context) error { return pgkit.Health(ctx, pool) })
	srv := httptest.NewServer(router)
	t.Cleanup(srv.Close)

	return &stack{baseURL: srv.URL, auth: a, store: st, cache: c, enrich: fe}
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
		ID: uuid.New(), UserID: userID, ProductID: ptr(uuid.New()),
		ItemType: "game", MediaType: "physical", DisplayName: "Chrono Trigger",
		Region: "ntsc_u", Packaging: "cib", Currency: "USD",
		PricingMode: "auto", Status: "backlog", BacklogRank: &rank,
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
		cust.IGDBGameID = ptr(int64(1000))
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
		cust.PricingProductID = ptr(uuid.New())
		cust.IGDBGameID = ptr(int64(1000))
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
		cust.PlatformName = ptr("SNES")
		cust.PlatformIGDBID = ptr(int64(19))
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
			p := enrichapi.Product{Id: id, Type: enrichapi.ProductType("game"),
				Igdb:     &enrichapi.IgdbMeta{GameId: gameID},
				Platform: &enrichapi.PlatformRef{IgdbPlatformId: platformID, Name: "SNES"}}
			if matched {
				p.Pricecharting = &enrichapi.PricechartingMeta{PcProductId: 5011}
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
			releaseDates := []enrichapi.ReleaseDate{
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
		p.Igdb.ReleaseDates = &[]enrichapi.ReleaseDate{
			{Region: "north_america", Date: openapi_types.Date{Time: naDate}},
			{Region: "europe", Date: openapi_types.Date{Time: euDate}},
		}
		return p
	}
	// gameBacked carries an igdb game id and an already-picked date -
	// the precondition for a region edit to be fetch-eligible at all.
	gameBacked := func() store.Entry {
		e := storedGameEntry(user)
		e.IGDBGameID = ptr(int64(1000))
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

// listedEntry builds a stored entry with knobs the list tests need.
func listedEntry(user uuid.UUID, name string, mut func(*store.Entry)) store.Entry {
	e := storedGameEntry(user)
	e.ID = uuid.New()
	e.ProductID = ptr(uuid.New())
	e.DisplayName = name
	if mut != nil {
		mut(&e)
	}
	return e
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

// TestUnitListEntries_ValueSortPagesAfterComposing guards a
// compose-before-slice regression: if the handler ever sliced the
// page before pricing and re-sorting the full filtered set, a
// high-value row sitting past the page boundary in store order would
// never surface at the top of a value-desc page. Store order here is
// the exact reverse of value order, and the priciest row sits last -
// two spots beyond a limit=2 page - so a slice-first bug drops it.
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
	st := &stubStore{listEntries: func(context.Context, uuid.UUID, store.Filters) ([]store.Entry, error) {
		return []store.Entry{e}, nil
	}}
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
	st := &stubStore{listEntries: func(context.Context, uuid.UUID, store.Filters) ([]store.Entry, error) {
		return []store.Entry{multi, bare}, nil
	}}
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

// TestUnitListEntries_GroupingDimensions covers the group_by
// dimensions beyond tag: each has its own catch-all label for entries
// missing the grouped field, and item_type (always populated) has
// none.
func TestUnitListEntries_GroupingDimensions(t *testing.T) {
	user := uuid.New()

	t.Run("platform: named groups ascending, Unknown last", func(t *testing.T) {
		snes := listedEntry(user, "SNES Game", func(e *store.Entry) {
			e.PricingMode = "disabled"
			e.PlatformName = ptr("SNES")
		})
		genesis := listedEntry(user, "Genesis Game", func(e *store.Entry) {
			e.PricingMode = "disabled"
			e.PlatformName = ptr("Genesis")
		})
		unknown := listedEntry(user, "No Platform Game", func(e *store.Entry) {
			e.PricingMode = "disabled"
			e.PlatformName = nil
		})
		st := &stubStore{listEntries: func(context.Context, uuid.UUID, store.Filters) ([]store.Entry, error) {
			return []store.Entry{snes, genesis, unknown}, nil
		}}
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
			e.StorageLocation = ptr("Closet")
		})
		shelf := listedEntry(user, "Shelf Game", func(e *store.Entry) {
			e.PricingMode = "disabled"
			e.StorageLocation = ptr("Shelf A")
		})
		unassigned := listedEntry(user, "No Location Game", func(e *store.Entry) {
			e.PricingMode = "disabled"
			e.StorageLocation = nil
		})
		st := &stubStore{listEntries: func(context.Context, uuid.UUID, store.Filters) ([]store.Entry, error) {
			return []store.Entry{closet, shelf, unassigned}, nil
		}}
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
		st := &stubStore{listEntries: func(context.Context, uuid.UUID, store.Filters) ([]store.Entry, error) {
			return []store.Entry{game, console, accessory}, nil
		}}
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
	st := &stubStore{listEntries: func(context.Context, uuid.UUID, store.Filters) ([]store.Entry, error) {
		return all, nil
	}}
	var calls int
	var capturedIDs []uuid.UUID
	enrich := &stubEnrichment{batchPrices: func(_ context.Context, _ string, ids []uuid.UUID) (map[string]enrichapi.ProductPrices, error) {
		calls++
		capturedIDs = append(capturedIDs, ids...)
		return map[string]enrichapi.ProductPrices{}, nil
	}}
	srv, a := newUnitServer(t, st, enrich, newStubCache())

	// The page (offset 2, limit 3) spans one entry of each pricing
	// mode: proxy, auto, and disabled.
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
	// override, auto through its own product, disabled contributes
	// none at all.
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

	// Neither the empty page nor the validation failure placed a
	// spurious batch call.
	if calls != 1 {
		t.Fatalf("no spurious batch calls: %d", calls)
	}
}

// TestUnitListEntries_OffsetOverflowClampsToEmptyPage pins that an
// absurd-but-contract-legal offset (the contract sets no upper bound)
// answers a normal empty page rather than overflowing the paging math.
func TestUnitListEntries_OffsetOverflowClampsToEmptyPage(t *testing.T) {
	user := uuid.New()
	only := listedEntry(user, "Only", nil)
	st := &stubStore{listEntries: func(context.Context, uuid.UUID, store.Filters) ([]store.Entry, error) {
		return []store.Entry{only}, nil
	}}
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

// TestUnitListEntries_DefaultSortAndOrder pins the unset-params
// default: created_at desc, and a default limit generous enough that
// a small result set reaches the response whole.
func TestUnitListEntries_DefaultSortAndOrder(t *testing.T) {
	user := uuid.New()
	ea := listedEntry(user, "A", func(e *store.Entry) { e.PricingMode = "disabled" })
	eb := listedEntry(user, "B", func(e *store.Entry) { e.PricingMode = "disabled" })
	ec := listedEntry(user, "C", func(e *store.Entry) { e.PricingMode = "disabled" })
	st := &stubStore{listEntries: func(_ context.Context, _ uuid.UUID, f store.Filters) ([]store.Entry, error) {
		if f.Sort != "created_at" || f.Order != "desc" {
			t.Fatalf("unset sort/order must default: %+v", f)
		}
		return []store.Entry{ea, eb, ec}, nil
	}}
	// batchPrices deliberately nil: every fixture is pricing-disabled,
	// so a call here would panic the stub.
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
	// The default limit (200) must not truncate a 3-row set, and the
	// default offset (0) must not skip any of it.
	if got.TotalCount != 3 || len(got.Entries) != 3 {
		t.Fatalf("default page: %+v", got)
	}
}

func TestUnitListEntries_BadEnumParam(t *testing.T) {
	srv, a := newUnitServer(t, &stubStore{}, &stubEnrichment{}, newStubCache())
	for _, q := range []string{"status=queued", "sort=alphabetical", "group_by=color", "order=up", "region=jp"} {
		resp := do(t, http.MethodGet, srv.URL+"/entries?"+q, a.token(t, uuid.NewString()), nil)
		wantProblem(t, resp, http.StatusBadRequest, "invalid_param")
	}
}

func TestUnitListEntries_Empty(t *testing.T) {
	st := &stubStore{listEntries: func(context.Context, uuid.UUID, store.Filters) ([]store.Entry, error) {
		return []store.Entry{}, nil
	}}
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

func TestUnitTags(t *testing.T) {
	user := uuid.New()
	tag := store.Tag{ID: uuid.New(), Name: "rpg", EntryCount: 2}

	t.Run("list", func(t *testing.T) {
		st := &stubStore{listTags: func(context.Context, uuid.UUID) ([]store.Tag, error) {
			return []store.Tag{tag}, nil
		}}
		srv, a := newUnitServer(t, st, &stubEnrichment{}, newStubCache())
		resp := do(t, http.MethodGet, srv.URL+"/tags", a.token(t, user.String()), nil)
		var got struct {
			Tags []struct {
				Name       string `json:"name"`
				EntryCount int    `json:"entry_count"`
			} `json:"tags"`
		}
		_ = json.NewDecoder(resp.Body).Decode(&got)
		if resp.StatusCode != http.StatusOK || len(got.Tags) != 1 || got.Tags[0].EntryCount != 2 {
			t.Fatalf("list: %d %+v", resp.StatusCode, got)
		}
	})

	t.Run("create 201, duplicate 409, invalid 400", func(t *testing.T) {
		st := &stubStore{createTag: func(_ context.Context, _ uuid.UUID, name string) (store.Tag, error) {
			if name == "taken" {
				return store.Tag{}, store.ErrNameTaken
			}
			return store.Tag{ID: uuid.New(), Name: name}, nil
		}}
		srv, a := newUnitServer(t, st, &stubEnrichment{}, newStubCache())
		tok := a.token(t, user.String())
		if resp := do(t, http.MethodPost, srv.URL+"/tags", tok, jsonBody(map[string]any{"name": "rpg"})); resp.StatusCode != http.StatusCreated {
			t.Fatalf("create: %d", resp.StatusCode)
		}
		wantProblem(t, do(t, http.MethodPost, srv.URL+"/tags", tok, jsonBody(map[string]any{"name": "taken"})),
			http.StatusConflict, "tag_exists")
		wantProblem(t, do(t, http.MethodPost, srv.URL+"/tags", tok, jsonBody(map[string]any{"name": "   "})),
			http.StatusBadRequest, "invalid_body")
		wantProblem(t, do(t, http.MethodPost, srv.URL+"/tags", tok, jsonBody(map[string]any{"name": strings.Repeat("x", 51)})),
			http.StatusBadRequest, "invalid_body")
	})

	// TestUnitTags/create_cap_exceeded_429 pins the per-user tag cap's
	// status, code, and detail (the delegated status/code choice mirrors
	// the social service's edge caps: 429 code cap_exceeded).
	t.Run("create cap exceeded 429", func(t *testing.T) {
		st := &stubStore{createTag: func(context.Context, uuid.UUID, string) (store.Tag, error) {
			return store.Tag{}, store.ErrUserTagCapExceeded
		}}
		srv, a := newUnitServer(t, st, &stubEnrichment{}, newStubCache())
		resp := do(t, http.MethodPost, srv.URL+"/tags", a.token(t, user.String()), jsonBody(map[string]any{"name": "one too many"}))
		if resp.StatusCode != http.StatusTooManyRequests {
			t.Fatalf("status: got %d, want 429", resp.StatusCode)
		}
		var p struct {
			Code   string `json:"code"`
			Detail string `json:"detail"`
		}
		if err := json.NewDecoder(resp.Body).Decode(&p); err != nil {
			t.Fatal(err)
		}
		if p.Code != "cap_exceeded" {
			t.Fatalf("code: got %q, want cap_exceeded", p.Code)
		}
		if p.Detail != "at most 200 tags per user; delete a tag to create another" {
			t.Fatalf("detail: got %q", p.Detail)
		}
	})

	t.Run("rename + delete map sentinels", func(t *testing.T) {
		st := &stubStore{
			renameTag: func(context.Context, uuid.UUID, uuid.UUID, string) (store.Tag, error) {
				return store.Tag{}, store.ErrNotFound
			},
			deleteTag: func(context.Context, uuid.UUID, uuid.UUID) error { return store.ErrNotFound },
		}
		srv, a := newUnitServer(t, st, &stubEnrichment{}, newStubCache())
		tok := a.token(t, user.String())
		wantProblem(t, do(t, http.MethodPut, srv.URL+"/tags/"+uuid.NewString(), tok, jsonBody(map[string]any{"name": "x"})),
			http.StatusNotFound, "tag_not_found")
		wantProblem(t, do(t, http.MethodDelete, srv.URL+"/tags/"+uuid.NewString(), tok, nil),
			http.StatusNotFound, "tag_not_found")
	})

	t.Run("rename success 200", func(t *testing.T) {
		tagID := uuid.New()
		renamedTag := store.Tag{ID: tagID, Name: "adventure", EntryCount: 5}
		st := &stubStore{renameTag: func(_ context.Context, _ uuid.UUID, id uuid.UUID, name string) (store.Tag, error) {
			if id == tagID && name == "adventure" {
				return renamedTag, nil
			}
			return store.Tag{}, store.ErrNotFound
		}}
		srv, a := newUnitServer(t, st, &stubEnrichment{}, newStubCache())
		resp := do(t, http.MethodPut, srv.URL+"/tags/"+tagID.String(), a.token(t, user.String()),
			jsonBody(map[string]any{"name": "adventure"}))
		if resp.StatusCode != http.StatusOK {
			t.Fatalf("rename: %d", resp.StatusCode)
		}
		var got struct {
			Name       string `json:"name"`
			EntryCount int    `json:"entry_count"`
		}
		_ = json.NewDecoder(resp.Body).Decode(&got)
		if got.Name != "adventure" || got.EntryCount != 5 {
			t.Fatalf("rename response: %+v", got)
		}
	})

	t.Run("rename name-taken 409", func(t *testing.T) {
		st := &stubStore{renameTag: func(_ context.Context, _ uuid.UUID, _ uuid.UUID, _ string) (store.Tag, error) {
			return store.Tag{}, store.ErrNameTaken
		}}
		srv, a := newUnitServer(t, st, &stubEnrichment{}, newStubCache())
		wantProblem(t, do(t, http.MethodPut, srv.URL+"/tags/"+uuid.NewString(), a.token(t, user.String()),
			jsonBody(map[string]any{"name": "existing"})),
			http.StatusConflict, "tag_exists")
	})

	t.Run("delete success 204", func(t *testing.T) {
		tagID := uuid.New()
		st := &stubStore{deleteTag: func(_ context.Context, _ uuid.UUID, id uuid.UUID) error {
			if id == tagID {
				return nil
			}
			return store.ErrNotFound
		}}
		srv, a := newUnitServer(t, st, &stubEnrichment{}, newStubCache())
		resp := do(t, http.MethodDelete, srv.URL+"/tags/"+tagID.String(), a.token(t, user.String()), nil)
		if resp.StatusCode != http.StatusNoContent {
			t.Fatalf("delete: %d", resp.StatusCode)
		}
	})
}

// TestUnitCreateTag_NameCapCountsRunesNotBytes pins that the 50-character
// cap counts runes, not bytes: 50 multibyte characters (each of these
// three bytes in UTF-8) is exactly at the cap and must pass; 51 fails.
func TestUnitCreateTag_NameCapCountsRunesNotBytes(t *testing.T) {
	st := &stubStore{createTag: func(_ context.Context, _ uuid.UUID, name string) (store.Tag, error) {
		return store.Tag{ID: uuid.New(), Name: name}, nil
	}}
	srv, a := newUnitServer(t, st, &stubEnrichment{}, newStubCache())
	tok := a.token(t, uuid.NewString())

	at50 := strings.Repeat("\u3042", 50) // 50 runes, 150 bytes
	resp := do(t, http.MethodPost, srv.URL+"/tags", tok, jsonBody(map[string]any{"name": at50}))
	if resp.StatusCode != http.StatusCreated {
		body, _ := io.ReadAll(resp.Body)
		t.Fatalf("50 runes at the cap must pass under rune counting: %d: %s", resp.StatusCode, body)
	}

	over50 := strings.Repeat("\u3042", 51)
	resp = do(t, http.MethodPost, srv.URL+"/tags", tok, jsonBody(map[string]any{"name": over50}))
	wantProblem(t, resp, http.StatusBadRequest, "invalid_body")
}

// manyUUIDStrings builds n distinct uuid strings for maxItems guard
// tests (the request bounds this task adds have no existing
// generator to reuse).
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

// TestUnitBulkUpdateEntries_TagCapExceededMapsTo400 pins this task's
// delegated status/code choice for the bulk per-entry tag cap: 400,
// code tag_cap_exceeded (distinct from the generic invalid_body every
// other bulk-update guard answers).
func TestUnitBulkUpdateEntries_TagCapExceededMapsTo400(t *testing.T) {
	st := &stubStore{bulkUpdateEntries: func(context.Context, uuid.UUID, []uuid.UUID, store.BulkActions) (int, error) {
		return 0, store.ErrTagCapExceeded
	}}
	srv, a := newUnitServer(t, st, &stubEnrichment{}, newStubCache())
	resp := do(t, http.MethodPost, srv.URL+"/entries/bulk-update", a.token(t, uuid.NewString()),
		jsonBody(map[string]any{"entry_ids": []string{uuid.NewString()}, "add_tag_ids": []string{uuid.NewString()}}))
	wantProblem(t, resp, http.StatusBadRequest, "tag_cap_exceeded")
}

func TestUnitViews(t *testing.T) {
	user := uuid.New()
	params := map[string]any{"filters": map[string]any{"status": []string{"backlog"}}, "view_mode": "grid"}

	t.Run("create round-trips params verbatim", func(t *testing.T) {
		var storedParams []byte
		st := &stubStore{createView: func(_ context.Context, _ uuid.UUID, name string, p []byte, _ string) (store.View, error) {
			storedParams = p
			return store.View{ID: uuid.New(), Name: name, Params: p}, nil
		}}
		srv, a := newUnitServer(t, st, &stubEnrichment{}, newStubCache())
		resp := do(t, http.MethodPost, srv.URL+"/views", a.token(t, user.String()),
			jsonBody(map[string]any{"name": "Backlog grid", "params": params}))
		if resp.StatusCode != http.StatusCreated {
			t.Fatalf("create: %d", resp.StatusCode)
		}
		var got struct {
			Params map[string]any `json:"params"`
		}
		_ = json.NewDecoder(resp.Body).Decode(&got)
		if got.Params["view_mode"] != "grid" || storedParams == nil {
			t.Fatalf("params round-trip: %+v / %s", got.Params, storedParams)
		}
	})

	t.Run("validation", func(t *testing.T) {
		// The empty stubStore proves the store is never reached: a
		// bad request must 400 before any of these get near it.
		srv, a := newUnitServer(t, &stubStore{}, &stubEnrichment{}, newStubCache())
		tok := a.token(t, user.String())
		wantProblem(t, do(t, http.MethodPost, srv.URL+"/views", tok, jsonBody(map[string]any{"name": "", "params": params})),
			http.StatusBadRequest, "invalid_body")
		// params too large: an ~9KB filler.
		big := map[string]any{"name": "big", "params": map[string]any{"blob": strings.Repeat("x", 9000)}}
		wantProblem(t, do(t, http.MethodPost, srv.URL+"/views", tok, jsonBody(big)),
			http.StatusBadRequest, "invalid_body")
		// visibility outside {private, unlisted, listed}: the generated
		// enum type is a plain string underneath (no UnmarshalJSON
		// validation), so an invalid value must be rejected here -
		// otherwise only the DB CHECK constraint would catch it and the
		// client would see a 500 instead of a 400.
		bad := map[string]any{"name": "x", "params": params, "visibility": "public"}
		wantProblem(t, do(t, http.MethodPost, srv.URL+"/views", tok, jsonBody(bad)),
			http.StatusBadRequest, "invalid_body")
	})

	t.Run("conflicts and not-found map", func(t *testing.T) {
		st := &stubStore{
			createView: func(context.Context, uuid.UUID, string, []byte, string) (store.View, error) {
				return store.View{}, store.ErrNameTaken
			},
			updateView: func(context.Context, uuid.UUID, uuid.UUID, string, []byte, string) (store.View, error) {
				return store.View{}, store.ErrNotFound
			},
			deleteView: func(context.Context, uuid.UUID, uuid.UUID) error { return store.ErrNotFound },
		}
		srv, a := newUnitServer(t, st, &stubEnrichment{}, newStubCache())
		tok := a.token(t, user.String())
		wantProblem(t, do(t, http.MethodPost, srv.URL+"/views", tok, jsonBody(map[string]any{"name": "x", "params": params})),
			http.StatusConflict, "view_exists")
		wantProblem(t, do(t, http.MethodPut, srv.URL+"/views/"+uuid.NewString(), tok, jsonBody(map[string]any{"name": "x", "params": params})),
			http.StatusNotFound, "view_not_found")
		wantProblem(t, do(t, http.MethodDelete, srv.URL+"/views/"+uuid.NewString(), tok, nil),
			http.StatusNotFound, "view_not_found")
	})

	t.Run("update success 200", func(t *testing.T) {
		viewID := uuid.New()
		newParams := map[string]any{"filters": map[string]any{"status": []string{"completed"}}, "view_mode": "list"}
		paramsJSON, _ := json.Marshal(newParams)
		updatedView := store.View{ID: viewID, Name: "Completed List", Params: paramsJSON}
		st := &stubStore{updateView: func(_ context.Context, _ uuid.UUID, id uuid.UUID, name string, p []byte, _ string) (store.View, error) {
			if id == viewID && name == "Completed List" {
				return updatedView, nil
			}
			return store.View{}, store.ErrNotFound
		}}
		srv, a := newUnitServer(t, st, &stubEnrichment{}, newStubCache())
		resp := do(t, http.MethodPut, srv.URL+"/views/"+viewID.String(), a.token(t, user.String()),
			jsonBody(map[string]any{"name": "Completed List", "params": newParams}))
		if resp.StatusCode != http.StatusOK {
			t.Fatalf("update: %d", resp.StatusCode)
		}
		var got struct {
			Name   string         `json:"name"`
			Params map[string]any `json:"params"`
		}
		_ = json.NewDecoder(resp.Body).Decode(&got)
		if got.Name != "Completed List" || got.Params["view_mode"] != "list" {
			t.Fatalf("update response: %+v", got)
		}
	})

	t.Run("update name-taken 409", func(t *testing.T) {
		st := &stubStore{updateView: func(_ context.Context, _ uuid.UUID, _ uuid.UUID, _ string, _ []byte, _ string) (store.View, error) {
			return store.View{}, store.ErrNameTaken
		}}
		srv, a := newUnitServer(t, st, &stubEnrichment{}, newStubCache())
		wantProblem(t, do(t, http.MethodPut, srv.URL+"/views/"+uuid.NewString(), a.token(t, user.String()),
			jsonBody(map[string]any{"name": "taken", "params": params})),
			http.StatusConflict, "view_exists")
	})

	t.Run("delete success 204", func(t *testing.T) {
		viewID := uuid.New()
		st := &stubStore{deleteView: func(_ context.Context, _ uuid.UUID, id uuid.UUID) error {
			if id == viewID {
				return nil
			}
			return store.ErrNotFound
		}}
		srv, a := newUnitServer(t, st, &stubEnrichment{}, newStubCache())
		resp := do(t, http.MethodDelete, srv.URL+"/views/"+viewID.String(), a.token(t, user.String()), nil)
		if resp.StatusCode != http.StatusNoContent {
			t.Fatalf("delete: %d", resp.StatusCode)
		}
	})

	t.Run("list seeds defaults on the zero-view case", func(t *testing.T) {
		u := uuid.New()
		var seedCalled bool
		var listCalls int
		defaults := []store.View{
			{ID: uuid.New(), UserID: u, Name: "All Games", Slug: "all_games", Params: []byte(`{}`)},
			{ID: uuid.New(), UserID: u, Name: "Backlog", Slug: "backlog", Params: []byte(`{}`)},
		}
		st := &stubStore{
			listViews: func(context.Context, uuid.UUID) ([]store.View, error) {
				listCalls++
				if listCalls == 1 {
					return nil, nil
				}
				return defaults, nil
			},
			seedDefaultViews: func(context.Context, uuid.UUID) error {
				seedCalled = true
				return nil
			},
		}
		srv, a := newUnitServer(t, st, &stubEnrichment{}, newStubCache())
		resp := do(t, http.MethodGet, srv.URL+"/views", a.token(t, u.String()), nil)
		if resp.StatusCode != http.StatusOK {
			t.Fatalf("status: %d", resp.StatusCode)
		}
		if !seedCalled {
			t.Fatal("the zero-view case must seed the starter shelves")
		}
		if listCalls != 2 {
			t.Fatalf("must re-list after seeding: %d calls", listCalls)
		}
		var got struct {
			Views []struct{ Name string } `json:"views"`
		}
		_ = json.NewDecoder(resp.Body).Decode(&got)
		if len(got.Views) != 2 {
			t.Fatalf("want the two seeded defaults back: %+v", got.Views)
		}
	})

	t.Run("list does not seed when views already exist", func(t *testing.T) {
		u := uuid.New()
		existing := []store.View{{ID: uuid.New(), UserID: u, Name: "Mine", Slug: "mine", Params: []byte(`{}`)}}
		// seedDefaultViews is intentionally left nil: a call panics
		// loudly, proving the non-empty case never seeds.
		st := &stubStore{listViews: func(context.Context, uuid.UUID) ([]store.View, error) {
			return existing, nil
		}}
		srv, a := newUnitServer(t, st, &stubEnrichment{}, newStubCache())
		resp := do(t, http.MethodGet, srv.URL+"/views", a.token(t, u.String()), nil)
		if resp.StatusCode != http.StatusOK {
			t.Fatalf("status: %d", resp.StatusCode)
		}
		var got struct {
			Views []struct{ Name string } `json:"views"`
		}
		_ = json.NewDecoder(resp.Body).Decode(&got)
		if len(got.Views) != 1 || got.Views[0].Name != "Mine" {
			t.Fatalf("want the existing view unchanged: %+v", got.Views)
		}
	})

	t.Run("list seed failure maps to 500", func(t *testing.T) {
		u := uuid.New()
		st := &stubStore{
			listViews:        func(context.Context, uuid.UUID) ([]store.View, error) { return nil, nil },
			seedDefaultViews: func(context.Context, uuid.UUID) error { return errors.New("seed boom") },
		}
		srv, a := newUnitServer(t, st, &stubEnrichment{}, newStubCache())
		resp := do(t, http.MethodGet, srv.URL+"/views", a.token(t, u.String()), nil)
		wantProblem(t, resp, http.StatusInternalServerError, "internal")
	})

	t.Run("list re-list failure maps to 500", func(t *testing.T) {
		u := uuid.New()
		var listCalls int
		st := &stubStore{
			listViews: func(context.Context, uuid.UUID) ([]store.View, error) {
				listCalls++
				if listCalls == 1 {
					return nil, nil
				}
				return nil, errors.New("relist boom")
			},
			seedDefaultViews: func(context.Context, uuid.UUID) error { return nil },
		}
		srv, a := newUnitServer(t, st, &stubEnrichment{}, newStubCache())
		resp := do(t, http.MethodGet, srv.URL+"/views", a.token(t, u.String()), nil)
		wantProblem(t, resp, http.StatusInternalServerError, "internal")
	})
}

func TestTagsAndViewsThroughTheStack(t *testing.T) {
	s := newStack(t)
	sub := uuid.New()
	tok := s.auth.token(t, sub.String())

	// Tag create; case-insensitive duplicate 409s via the real citext index.
	resp := do(t, http.MethodPost, s.baseURL+"/tags", tok, jsonBody(map[string]any{"name": "RPG"}))
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("tag create: %d", resp.StatusCode)
	}
	wantProblem(t, do(t, http.MethodPost, s.baseURL+"/tags", tok, jsonBody(map[string]any{"name": "rpg"})),
		http.StatusConflict, "tag_exists")

	// View params survive jsonb round-trip byte-for-byte semantically.
	params := map[string]any{"filters": map[string]any{"tag_id": []string{uuid.NewString()}}, "sort": "value"}
	resp = do(t, http.MethodPost, s.baseURL+"/views", tok, jsonBody(map[string]any{"name": "Valuable", "params": params}))
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("view create: %d", resp.StatusCode)
	}
	resp = do(t, http.MethodGet, s.baseURL+"/views", tok, nil)
	var got struct {
		Views []struct {
			Name   string         `json:"name"`
			Params map[string]any `json:"params"`
		} `json:"views"`
	}
	_ = json.NewDecoder(resp.Body).Decode(&got)
	if len(got.Views) != 1 || got.Views[0].Params["sort"] != "value" {
		t.Fatalf("view round-trip: %+v", got.Views)
	}
}

func dashboardStore(user uuid.UUID, rows []store.PricingRow) *stubStore {
	return &stubStore{
		dashboardCounts: func(context.Context, uuid.UUID, store.Filters) (store.DashboardCounts, error) {
			return store.DashboardCounts{
				Total:      len(rows),
				ByStatus:   map[string]int{"backlog": len(rows)},
				ByItemType: map[string]int{"game": len(rows)},
				ByPlatform: []store.PlatformCount{{Name: "SNES", Count: len(rows) - 1}, {Name: "", Count: 1}},
				Spend:      []store.CurrencySpend{{Currency: "USD", TotalCents: 5000}},
			}, nil
		},
		pricingRows: func(context.Context, uuid.UUID, store.Filters) ([]store.PricingRow, error) { return rows, nil },
	}
}

func TestUnitDashboard_ComposesAndCaches(t *testing.T) {
	user := uuid.New()
	auto := store.PricingRow{EntryID: uuid.New(), Packaging: "cib", PricingMode: "auto", ProductID: ptr(uuid.New())}
	proxyTarget := uuid.New()
	// A custom entry: no own product, priced through the proxy.
	proxy := store.PricingRow{EntryID: uuid.New(), Packaging: "loose", PricingMode: "proxy", PricingProductID: &proxyTarget}
	disabled := store.PricingRow{EntryID: uuid.New(), Packaging: "cib", PricingMode: "disabled", ProductID: ptr(uuid.New())}
	unpriced := store.PricingRow{EntryID: uuid.New(), Packaging: "sealed", PricingMode: "auto", ProductID: ptr(uuid.New())}

	st := dashboardStore(user, []store.PricingRow{auto, proxy, disabled, unpriced})
	enrich := &stubEnrichment{batchPrices: func(_ context.Context, _ string, ids []uuid.UUID) (map[string]enrichapi.ProductPrices, error) {
		cib, loose := int64(4200), int64(1500)
		return map[string]enrichapi.ProductPrices{
			auto.ProductID.String(): {CibCents: &cib},
			proxyTarget.String():    {LooseCents: &loose},
			// unpriced.ProductID absent: unknown to the catalog map.
		}, nil
	}}
	c := newStubCache()
	srv, a := newUnitServer(t, st, enrich, c)

	resp := do(t, http.MethodGet, srv.URL+"/dashboard", a.token(t, user.String()), nil)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status %d", resp.StatusCode)
	}
	var got struct {
		TotalEntries int                     `json:"total_entries"`
		ByPlatform   []struct{ Name string } `json:"by_platform"`
		Pricing      struct {
			Available       bool   `json:"available"`
			TotalValueCents *int64 `json:"total_value_cents"`
			PricedEntries   int    `json:"priced_entries"`
			UnpricedEntries int    `json:"unpriced_entries"`
			ExcludedEntries int    `json:"excluded_entries"`
		} `json:"pricing"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&got); err != nil {
		t.Fatal(err)
	}
	// auto cib 4200 + proxy loose 1500; the absent product is unpriced;
	// disabled is excluded; "" platform reads Unknown.
	if got.TotalEntries != 4 || !got.Pricing.Available ||
		got.Pricing.TotalValueCents == nil || *got.Pricing.TotalValueCents != 5700 ||
		got.Pricing.PricedEntries != 2 || got.Pricing.UnpricedEntries != 1 || got.Pricing.ExcludedEntries != 1 {
		t.Fatalf("dashboard: %+v", got)
	}
	if got.ByPlatform[1].Name != "Unknown" {
		t.Fatalf("platformless label: %+v", got.ByPlatform)
	}
	// The composed body was cached for this user.
	if c.bodies[user.String()] == nil {
		t.Fatal("a healthy dashboard must be cached")
	}
}

func TestUnitDashboard_CacheHitShortCircuits(t *testing.T) {
	user := uuid.New()
	c := newStubCache()
	c.bodies[user.String()] = []byte(`{"total_entries":42}`)
	// A store or enrichment call would panic (all fields nil): the hit
	// must answer alone.
	srv, a := newUnitServer(t, &stubStore{}, &stubEnrichment{}, c)
	resp := do(t, http.MethodGet, srv.URL+"/dashboard", a.token(t, user.String()), nil)
	body, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != http.StatusOK || string(body) != `{"total_entries":42}` {
		t.Fatalf("cache hit: %d %s", resp.StatusCode, body)
	}
}

func TestUnitDashboard_DegradedIsNotCached(t *testing.T) {
	user := uuid.New()
	rows := []store.PricingRow{{EntryID: uuid.New(), Packaging: "cib", PricingMode: "auto", ProductID: ptr(uuid.New())}}
	st := dashboardStore(user, rows)
	enrich := &stubEnrichment{batchPrices: func(context.Context, string, []uuid.UUID) (map[string]enrichapi.ProductPrices, error) {
		return nil, enrichmentclient.ErrUnavailable
	}}
	c := newStubCache()
	srv, a := newUnitServer(t, st, enrich, c)
	resp := do(t, http.MethodGet, srv.URL+"/dashboard", a.token(t, user.String()), nil)
	var got struct {
		Pricing struct {
			Available bool `json:"available"`
		} `json:"pricing"`
	}
	_ = json.NewDecoder(resp.Body).Decode(&got)
	if resp.StatusCode != http.StatusOK || got.Pricing.Available {
		t.Fatalf("degraded dashboard: %d %+v", resp.StatusCode, got)
	}
	if c.bodies[user.String()] != nil {
		t.Fatal("a degraded dashboard must NOT be cached (it would hide recovery)")
	}
}

func TestUnitDashboard_CacheErrorsFailOpen(t *testing.T) {
	user := uuid.New()
	st := dashboardStore(user, nil)
	c := newStubCache()
	c.err = errors.New("valkey is having a moment")
	srv, a := newUnitServer(t, st, &stubEnrichment{}, c)
	resp := do(t, http.MethodGet, srv.URL+"/dashboard", a.token(t, user.String()), nil)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("cache failure must not fail the dashboard: %d", resp.StatusCode)
	}
}

func TestUnitDashboard_FilteredComputesLiveAndSkipsCache(t *testing.T) {
	user := uuid.New()
	var gotCounts, gotRows *store.Filters
	st := &stubStore{
		dashboardCounts: func(_ context.Context, _ uuid.UUID, f store.Filters) (store.DashboardCounts, error) {
			gotCounts = &f
			return store.DashboardCounts{
				Total:      2,
				ByStatus:   map[string]int{"backlog": 2},
				ByItemType: map[string]int{"game": 2},
				ByPlatform: []store.PlatformCount{{Name: "SNES", Count: 2}},
				Spend:      []store.CurrencySpend{},
			}, nil
		},
		pricingRows: func(_ context.Context, _ uuid.UUID, f store.Filters) ([]store.PricingRow, error) {
			gotRows = &f
			return []store.PricingRow{}, nil
		},
	}
	c := newStubCache()
	// A cached unfiltered dashboard must never answer a filtered
	// request (and the filtered result must not replace it).
	sentinel := []byte(`{"total_entries":999}`)
	c.bodies[user.String()] = sentinel
	srv, a := newUnitServer(t, st, &stubEnrichment{}, c)

	resp := do(t, http.MethodGet, srv.URL+"/dashboard?status=backlog&platform_id=19", a.token(t, user.String()), nil)
	var got struct {
		TotalEntries int `json:"total_entries"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&got); err != nil {
		t.Fatal(err)
	}
	if resp.StatusCode != http.StatusOK || got.TotalEntries != 2 {
		t.Fatalf("filtered dashboard: %d %+v", resp.StatusCode, got)
	}
	if gotCounts == nil || gotRows == nil {
		t.Fatal("both aggregates must run for a filtered request")
	}
	for _, f := range []*store.Filters{gotCounts, gotRows} {
		if len(f.Statuses) != 1 || f.Statuses[0] != "backlog" ||
			len(f.PlatformIDs) != 1 || f.PlatformIDs[0] != 19 {
			t.Fatalf("filters did not reach the store: %+v", f)
		}
	}
	if string(c.bodies[user.String()]) != string(sentinel) {
		t.Fatal("a filtered result must not overwrite the unfiltered cache")
	}
}

func TestUnitDashboard_BadFilterRejected(t *testing.T) {
	// Zero-field stubs prove the 400 answers before any store, cache,
	// or enrichment work.
	srv, a := newUnitServer(t, &stubStore{}, &stubEnrichment{}, &stubCache{})
	for _, q := range []string{"status=queued", "item_type=chair", "region=jp"} {
		resp := do(t, http.MethodGet, srv.URL+"/dashboard?"+q, a.token(t, uuid.NewString()), nil)
		wantProblem(t, resp, http.StatusBadRequest, "invalid_param")
	}
}

func TestUnitLibrarySummary(t *testing.T) {
	user := uuid.New()
	rating := 8
	st := &stubStore{librarySummary: func(context.Context, uuid.UUID) ([]store.LibraryGame, error) {
		return []store.LibraryGame{
			{IGDBGameID: 2000, Rating: &rating, AllDropped: false},
			{IGDBGameID: 2001, AllDropped: true},
		}, nil
	}}
	srv, a := newUnitServer(t, st, &stubEnrichment{}, newStubCache())
	resp := do(t, http.MethodGet, srv.URL+"/library/summary", a.token(t, user.String()), nil)
	var got struct {
		Library []struct {
			IgdbGameID int64   `json:"igdb_game_id"`
			Rating     *int    `json:"rating"`
			Status     *string `json:"status"`
		} `json:"library"`
	}
	_ = json.NewDecoder(resp.Body).Decode(&got)
	if len(got.Library) != 2 ||
		got.Library[0].IgdbGameID != 2000 || *got.Library[0].Rating != 8 || got.Library[0].Status != nil ||
		got.Library[1].Status == nil || *got.Library[1].Status != "dropped" {
		t.Fatalf("library: %+v", got.Library)
	}
}

func TestDashboardInvalidationThroughTheStack(t *testing.T) {
	s := newStack(t)
	productID := s.enrich.addGame("Chrono Trigger", 1500, 4200, 9900)
	sub := uuid.New()
	tok := s.auth.token(t, sub.String())

	dashboard := func() (int, bool) {
		t.Helper()
		resp := do(t, http.MethodGet, s.baseURL+"/dashboard", tok, nil)
		if resp.StatusCode != http.StatusOK {
			t.Fatalf("dashboard: %d", resp.StatusCode)
		}
		var got struct {
			TotalEntries int `json:"total_entries"`
			Pricing      struct {
				Available bool `json:"available"`
			} `json:"pricing"`
		}
		_ = json.NewDecoder(resp.Body).Decode(&got)
		return got.TotalEntries, got.Pricing.Available
	}

	if n, _ := dashboard(); n != 0 {
		t.Fatalf("empty collection: %d", n)
	}
	// A second read is served from Valkey: the enrichment fake sees no
	// second batch call.
	before := s.enrich.batchHits
	if n, _ := dashboard(); n != 0 {
		t.Fatalf("cached read: %d", n)
	}
	if s.enrich.batchHits != before {
		t.Fatalf("second read must come from the cache (batch hits %d -> %d)", before, s.enrich.batchHits)
	}

	// A mutation invalidates: the next read recomposes and sees the row.
	resp := do(t, http.MethodPost, s.baseURL+"/entries", tok, createBody(productID, nil))
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("create: %d", resp.StatusCode)
	}
	if n, _ := dashboard(); n != 1 {
		t.Fatalf("post-mutation dashboard must recompose, got %d entries", n)
	}

	// Degraded pricing is never cached: recovery is visible on the very
	// next read.
	s.enrich.mu.Lock()
	s.enrich.down = true
	s.enrich.mu.Unlock()
	// Invalidate via another mutation so the next read recomposes. A
	// product-backed create cannot be used here: it fetches the product
	// from enrichment (a hard dependency), which is down too. A custom
	// (non-proxied) create has no enrichment dependency and still
	// invalidates the dashboard cache like any other mutation.
	resp = do(t, http.MethodPost, s.baseURL+"/entries", tok, jsonBody(map[string]any{
		"display_name": "Offline pickup", "item_type": "game",
		"region": "ntsc_u", "packaging": "loose",
	}))
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("create: %d", resp.StatusCode)
	}
	if _, available := dashboard(); available {
		t.Fatal("pricing must be degraded")
	}
	s.enrich.mu.Lock()
	s.enrich.down = false
	s.enrich.mu.Unlock()
	if _, available := dashboard(); !available {
		t.Fatal("recovery must be immediate (degraded results are not cached)")
	}
}

func TestComposeValueSeries_CustomStepsInAtSetAt(t *testing.T) {
	day := func(s string) time.Time {
		d, err := time.Parse(time.RFC3339, s)
		if err != nil {
			t.Fatal(err)
		}
		return d
	}
	windowStart := day("2026-06-01T00:00:00Z")
	v := int64(5000)
	setAt := day("2026-06-10T06:00:00Z") // mid-day: truncates to 06-10
	customRow := store.PricingRow{EntryID: uuid.New(), Packaging: "loose",
		PricingMode: "custom", CustomValueCents: &v, CustomValueSetAt: &setAt}

	pid := uuid.New()
	loose1, loose2 := int64(1000), int64(1200)
	proxyRow := store.PricingRow{EntryID: uuid.New(), Packaging: "loose",
		PricingMode: "proxy", PricingProductID: &pid}
	series := map[string][]enrichapi.PricePoint{pid.String(): {
		{CapturedAt: day("2026-06-05T06:00:00Z"), LooseCents: &loose1},
		{CapturedAt: day("2026-06-15T06:00:00Z"), LooseCents: &loose2},
	}}

	points := server.ComposeValueSeries([]store.PricingRow{customRow, proxyRow}, series, windowStart)
	// Days: 06-05 (snapshot), 06-10 (set-at injected), 06-15 (snapshot).
	if len(points) != 3 {
		t.Fatalf("want 3 points, got %d: %+v", len(points), points)
	}
	wants := []int64{1000, 6000, 6200} // custom joins on 06-10, carry-forward everywhere
	for i, w := range wants {
		if points[i].ValueCents != w {
			t.Fatalf("point %d = %d, want %d", i, points[i].ValueCents, w)
		}
	}
}

func TestComposeValueSeries_CustomOnlyAndPreWindowClamp(t *testing.T) {
	day := func(s string) time.Time {
		d, _ := time.Parse(time.RFC3339, s)
		return d
	}
	windowStart := day("2026-06-01T00:00:00Z")
	v := int64(7700)
	old := day("2026-01-15T12:00:00Z") // long before the window
	row := store.PricingRow{EntryID: uuid.New(), Packaging: "cib",
		PricingMode: "custom", CustomValueCents: &v, CustomValueSetAt: &old}

	points := server.ComposeValueSeries([]store.PricingRow{row}, nil, windowStart)
	if len(points) != 1 {
		t.Fatalf("want the clamped set-at day only, got %d points", len(points))
	}
	if !points[0].Date.Equal(windowStart) || points[0].ValueCents != 7700 {
		t.Fatalf("want 7700 at %v, got %+v", windowStart, points[0])
	}
}

func TestComposeValueSeries_SetAtAtMidnightBoundary(t *testing.T) {
	day := func(s string) time.Time {
		d, _ := time.Parse(time.RFC3339, s)
		return d
	}
	windowStart := day("2026-06-01T00:00:00Z")
	v := int64(100)
	exact := day("2026-06-10T00:00:00Z") // exactly midnight: contributes ON its day
	row := store.PricingRow{EntryID: uuid.New(), Packaging: "loose",
		PricingMode: "custom", CustomValueCents: &v, CustomValueSetAt: &exact}
	points := server.ComposeValueSeries([]store.PricingRow{row}, nil, windowStart)
	if len(points) != 1 || points[0].ValueCents != 100 || !points[0].Date.Equal(exact) {
		t.Fatalf("midnight set-at must contribute on its own day, got %+v", points)
	}
}

// TestComposeValueSeries_SnapshotCapturedAtMidnightBoundary covers the
// other captured-at input (a product snapshot, not a custom set-at):
// existing snapshot fixtures all use 06:00Z, so pin the exact-midnight
// case too.
func TestComposeValueSeries_SnapshotCapturedAtMidnightBoundary(t *testing.T) {
	day := func(s string) time.Time {
		d, _ := time.Parse(time.RFC3339, s)
		return d
	}
	windowStart := day("2026-06-01T00:00:00Z")
	pid := uuid.New()
	loose := int64(1000)
	exact := day("2026-06-10T00:00:00Z") // exactly midnight, not the fixtures' usual 06:00Z
	row := store.PricingRow{EntryID: uuid.New(), Packaging: "loose",
		PricingMode: "proxy", PricingProductID: &pid}
	series := map[string][]enrichapi.PricePoint{pid.String(): {{CapturedAt: exact, LooseCents: &loose}}}

	points := server.ComposeValueSeries([]store.PricingRow{row}, series, windowStart)
	if len(points) != 1 || !points[0].Date.Equal(exact) || points[0].ValueCents != 1000 {
		t.Fatalf("midnight snapshot must bucket into and price its own day, got %+v", points)
	}
}

// TestComposeValueSeries_SortsShuffledSeriesDefensively pins that
// composition does not depend on the caller delivering each product's
// snapshots oldest-first: a shuffled series composes identically to
// the same points pre-sorted.
func TestComposeValueSeries_SortsShuffledSeriesDefensively(t *testing.T) {
	day := func(s string) time.Time {
		d, _ := time.Parse(time.RFC3339, s)
		return d
	}
	windowStart := day("2026-06-01T00:00:00Z")
	pid := uuid.New()
	row := store.PricingRow{EntryID: uuid.New(), Packaging: "loose",
		PricingMode: "proxy", PricingProductID: &pid}

	l1, l2, l3 := int64(1000), int64(1200), int64(1500)
	oldest := enrichapi.PricePoint{CapturedAt: day("2026-06-05T06:00:00Z"), LooseCents: &l1}
	middle := enrichapi.PricePoint{CapturedAt: day("2026-06-10T06:00:00Z"), LooseCents: &l2}
	newest := enrichapi.PricePoint{CapturedAt: day("2026-06-20T06:00:00Z"), LooseCents: &l3}

	want := server.ComposeValueSeries([]store.PricingRow{row},
		map[string][]enrichapi.PricePoint{pid.String(): {oldest, middle, newest}}, windowStart)
	got := server.ComposeValueSeries([]store.PricingRow{row},
		map[string][]enrichapi.PricePoint{pid.String(): {newest, oldest, middle}}, windowStart)

	if len(got) != len(want) {
		t.Fatalf("shuffled input produced %d points, want %d", len(got), len(want))
	}
	for i := range want {
		if !got[i].Date.Equal(want[i].Date.Time) || got[i].ValueCents != want[i].ValueCents {
			t.Fatalf("point %d: shuffled gave %+v, sorted-input gave %+v", i, got[i], want[i])
		}
	}
}

// The pricing rows seed entries the same way the dashboard unit tests
// do; the enrichment stub answers per-product snapshot series.
func TestUnitValueHistoryComposesCarriesForwardAndCaches(t *testing.T) {
	userID := uuid.New()
	own := uuid.New()
	proxyTarget := uuid.New()
	day := func(d int) time.Time { return time.Date(2026, time.July, d, 6, 0, 0, 0, time.UTC) }
	cents := func(v int64) *int64 { return &v }

	pricingCalls := 0
	st := &stubStore{
		pricingRows: func(context.Context, uuid.UUID, store.Filters) ([]store.PricingRow, error) {
			pricingCalls++
			return []store.PricingRow{
				// auto + cib: follows cib_cents
				{EntryID: uuid.New(), Packaging: "cib", PricingMode: "auto", ProductID: &own},
				// custom + proxy + loose: follows the target's loose_cents
				{EntryID: uuid.New(), Packaging: "loose", PricingMode: "proxy", PricingProductID: &proxyTarget},
				// disabled: excluded entirely
				{EntryID: uuid.New(), Packaging: "sealed", PricingMode: "disabled", ProductID: &own},
			}, nil
		},
	}
	var gotDays, gotIDs int
	enrich := &stubEnrichment{
		priceHistory: func(_ context.Context, _ string, ids []uuid.UUID, days int) (map[string][]enrichapi.PricePoint, error) {
			gotDays, gotIDs = days, len(ids)
			return map[string][]enrichapi.PricePoint{
				// own: points on day 1 and day 3
				own.String(): {
					{CapturedAt: day(1), CibCents: cents(4200)},
					{CapturedAt: day(3), CibCents: cents(4400)},
				},
				// proxy target: first point on day 2 (contributes 0 on day 1)
				proxyTarget.String(): {
					{CapturedAt: day(2), LooseCents: cents(1500)},
				},
			}, nil
		},
	}
	c := newStubCache()
	srv, a := newUnitServer(t, st, enrich, c)
	tok := a.token(t, userID.String())

	resp := do(t, http.MethodGet, srv.URL+"/dashboard/value-history", tok, nil)
	body, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status %d: %s", resp.StatusCode, body)
	}
	if gotDays != 90 {
		t.Fatalf("window: got %d days, want 90", gotDays)
	}
	if gotIDs != 2 {
		t.Fatalf("effective ids: got %d, want 2 (disabled row excluded)", gotIDs)
	}
	var got struct {
		Available bool `json:"available"`
		Points    []struct {
			Date       string `json:"date"`
			ValueCents int64  `json:"value_cents"`
		} `json:"points"`
	}
	if err := json.Unmarshal(body, &got); err != nil {
		t.Fatal(err)
	}
	if !got.Available || len(got.Points) != 3 {
		t.Fatalf("want available with 3 daily points, got %s", body)
	}
	// day1: own cib 4200 (proxy has no point yet) = 4200
	// day2: own carries 4200 forward + proxy loose 1500 = 5700
	// day3: own 4400 + proxy carried 1500 = 5900
	wants := []struct {
		date  string
		cents int64
	}{{"2026-07-01", 4200}, {"2026-07-02", 5700}, {"2026-07-03", 5900}}
	for i, w := range wants {
		if got.Points[i].Date != w.date || got.Points[i].ValueCents != w.cents {
			t.Fatalf("point %d: got %s=%d, want %s=%d",
				i, got.Points[i].Date, got.Points[i].ValueCents, w.date, w.cents)
		}
	}
	// The composed body was cached under the value-history key...
	if c.vhBodies[userID.String()] == nil {
		t.Fatal("available result must be cached")
	}
	// ...and a second read serves the cache without recomposing.
	pricingCalls = 0
	resp2 := do(t, http.MethodGet, srv.URL+"/dashboard/value-history", tok, nil)
	body2, _ := io.ReadAll(resp2.Body)
	if resp2.StatusCode != http.StatusOK || string(body2) != string(body) {
		t.Fatalf("cache hit must serve the identical body")
	}
	if pricingCalls != 0 {
		t.Fatal("cache hit must not recompose")
	}
}

func TestUnitValueHistoryDegradedIsServedNotCached(t *testing.T) {
	userID := uuid.New()
	own := uuid.New()
	st := &stubStore{
		pricingRows: func(context.Context, uuid.UUID, store.Filters) ([]store.PricingRow, error) {
			return []store.PricingRow{{EntryID: uuid.New(), Packaging: "cib", PricingMode: "auto", ProductID: &own}}, nil
		},
	}
	enrich := &stubEnrichment{
		priceHistory: func(context.Context, string, []uuid.UUID, int) (map[string][]enrichapi.PricePoint, error) {
			return nil, errors.New("enrichment down")
		},
	}
	c := newStubCache()
	srv, a := newUnitServer(t, st, enrich, c)

	resp := do(t, http.MethodGet, srv.URL+"/dashboard/value-history", a.token(t, userID.String()), nil)
	body, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status %d: %s", resp.StatusCode, body)
	}
	if !strings.Contains(string(body), `"available":false`) {
		t.Fatalf("degraded answer must say available=false: %s", body)
	}
	if len(c.vhBodies) != 0 {
		t.Fatal("degraded answers are never cached")
	}
}

func TestUnitValueHistoryEmptyCollection(t *testing.T) {
	userID := uuid.New()
	st := &stubStore{
		pricingRows: func(context.Context, uuid.UUID, store.Filters) ([]store.PricingRow, error) {
			return []store.PricingRow{}, nil
		},
	}
	// priceHistory deliberately nil: a call would panic the stub.
	srv, a := newUnitServer(t, st, &stubEnrichment{}, newStubCache())
	resp := do(t, http.MethodGet, srv.URL+"/dashboard/value-history", a.token(t, userID.String()), nil)
	body, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status %d: %s", resp.StatusCode, body)
	}
	if !strings.Contains(string(body), `"points":[]`) || !strings.Contains(string(body), `"available":true`) {
		t.Fatalf("empty collection: want available with empty points, got %s", body)
	}
}

func TestValueHistoryInvalidationThroughTheStack(t *testing.T) {
	s := newStack(t)
	productID := s.enrich.addGame("Chrono Trigger", 1500, 4200, 9900)
	sub := uuid.New()
	tok := s.auth.token(t, sub.String())

	// Cold read: empty collection composes an available, empty series
	// and caches it in the real Valkey.
	resp := do(t, http.MethodGet, s.baseURL+"/dashboard/value-history", tok, nil)
	body, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != http.StatusOK || !strings.Contains(string(body), `"points":[]`) {
		t.Fatalf("cold read: %d %s", resp.StatusCode, body)
	}

	// A create must invalidate it in Valkey, so the next read sees the
	// new entry's point (cib packaging -> 4200).
	resp = do(t, http.MethodPost, s.baseURL+"/entries", tok, createBody(productID, nil))
	if resp.StatusCode != http.StatusCreated {
		b, _ := io.ReadAll(resp.Body)
		t.Fatalf("create: %d %s", resp.StatusCode, b)
	}
	resp = do(t, http.MethodGet, s.baseURL+"/dashboard/value-history", tok, nil)
	body, _ = io.ReadAll(resp.Body)
	if !strings.Contains(string(body), `"value_cents":4200`) {
		t.Fatalf("post-create read must recompose with the new entry: %s", body)
	}
}

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
		}, "custom_value_cents must not be negative"},
		{"custom value over cap", func(m map[string]any) {
			m["pricing_mode"] = "custom"
			m["custom_value_cents"] = 1000000001
		}, "custom_value_cents must not exceed 1000000000"},
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
		}, "custom_value_entered_cents must not be negative"},
		{"entered cents over cap", func(m map[string]any) {
			m["pricing_mode"] = "custom"
			m["custom_value_cents"] = 5400
			m["custom_value_entered_cents"] = 1000000001
			m["custom_value_entered_currency"] = "EUR"
		}, "custom_value_entered_cents must not exceed 1000000000"},
		{"entered currency malformed", func(m map[string]any) {
			m["pricing_mode"] = "custom"
			m["custom_value_cents"] = 5400
			m["custom_value_entered_cents"] = 6000
			m["custom_value_entered_currency"] = "eur"
		}, "custom_value_entered_currency must be a 3-letter uppercase code"},
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
		e.CustomValueSetAt = ptr(time.Now())
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
			e.CustomValueSetAt = ptr(time.Now()) // the store SQL stamps this server-side
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
			e.CustomValueSetAt = ptr(time.Now())
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

// TestUnitListEntries_MixedCustomAndProxyComposition pins that a
// custom-priced entry is composed alongside an enrichment-priced one
// in the same list, and the two sort correctly against each other.
func TestUnitListEntries_MixedCustomAndProxyComposition(t *testing.T) {
	user := uuid.New()
	proxyTarget := uuid.New()
	proxied := listedEntry(user, "Proxied", func(e *store.Entry) {
		e.PricingMode = "proxy"
		e.PricingProductID = &proxyTarget
	})
	custom := listedEntry(user, "Custom", func(e *store.Entry) {
		e.PricingMode = "custom"
		e.CustomValueCents = ptr(int64(5000))
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

func TestUnitDashboard_CustomOnlyNeedsNoEnrichment(t *testing.T) {
	user := uuid.New()
	rows := []store.PricingRow{
		{EntryID: uuid.New(), PricingMode: "custom", CustomValueCents: ptr(int64(7700)), CustomValueSetAt: ptr(time.Now())},
	}
	st := dashboardStore(user, rows)
	enrich := &stubEnrichment{batchPrices: func(context.Context, string, []uuid.UUID) (map[string]enrichapi.ProductPrices, error) {
		t.Fatal("enrichment must not be consulted for custom-only pricing")
		return nil, nil
	}}
	srv, a := newUnitServer(t, st, enrich, newStubCache())

	resp := do(t, http.MethodGet, srv.URL+"/dashboard", a.token(t, user.String()), nil)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status %d", resp.StatusCode)
	}
	var got struct {
		Pricing struct {
			Available       bool   `json:"available"`
			TotalValueCents *int64 `json:"total_value_cents"`
			PricedEntries   int    `json:"priced_entries"`
			UnpricedEntries int    `json:"unpriced_entries"`
			ExcludedEntries int    `json:"excluded_entries"`
		} `json:"pricing"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&got); err != nil {
		t.Fatal(err)
	}
	if !got.Pricing.Available || got.Pricing.TotalValueCents == nil || *got.Pricing.TotalValueCents != 7700 ||
		got.Pricing.PricedEntries != 1 || got.Pricing.UnpricedEntries != 0 || got.Pricing.ExcludedEntries != 0 {
		t.Fatalf("dashboard: %+v", got.Pricing)
	}
}

func TestUnitDashboard_MixedCustomProxyDisabled(t *testing.T) {
	user := uuid.New()
	proxyTarget := uuid.New()
	custom := store.PricingRow{EntryID: uuid.New(), PricingMode: "custom", CustomValueCents: ptr(int64(7700)), CustomValueSetAt: ptr(time.Now())}
	proxy := store.PricingRow{EntryID: uuid.New(), Packaging: "loose", PricingMode: "proxy", PricingProductID: &proxyTarget}
	disabled := store.PricingRow{EntryID: uuid.New(), Packaging: "cib", PricingMode: "disabled", ProductID: ptr(uuid.New())}
	st := dashboardStore(user, []store.PricingRow{custom, proxy, disabled})
	enrich := &stubEnrichment{batchPrices: func(_ context.Context, _ string, ids []uuid.UUID) (map[string]enrichapi.ProductPrices, error) {
		if len(ids) != 1 || ids[0] != proxyTarget {
			t.Fatalf("effective ids: %v", ids)
		}
		loose := int64(1000)
		return map[string]enrichapi.ProductPrices{proxyTarget.String(): {LooseCents: &loose}}, nil
	}}
	srv, a := newUnitServer(t, st, enrich, newStubCache())

	resp := do(t, http.MethodGet, srv.URL+"/dashboard", a.token(t, user.String()), nil)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status %d", resp.StatusCode)
	}
	var got struct {
		Pricing struct {
			TotalValueCents *int64 `json:"total_value_cents"`
			PricedEntries   int    `json:"priced_entries"`
			UnpricedEntries int    `json:"unpriced_entries"`
			ExcludedEntries int    `json:"excluded_entries"`
		} `json:"pricing"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&got); err != nil {
		t.Fatal(err)
	}
	if got.Pricing.TotalValueCents == nil || *got.Pricing.TotalValueCents != 8700 ||
		got.Pricing.PricedEntries != 2 || got.Pricing.UnpricedEntries != 0 || got.Pricing.ExcludedEntries != 1 {
		t.Fatalf("dashboard: %+v", got.Pricing)
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
			e.CustomValueSetAt = ptr(time.Now())
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
				return enrichapi.Product{Id: id, Type: enrichapi.ProductType("pc_listing"), Name: "eBay: repro cart"}, nil
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

// ---- InternalResnapshot ----

// gameProductWithDates builds a game product carrying the given scalar
// first_release_date plus optional per-region IGDB dates, driving
// pickReleaseDate's region-chain resolution in the tests below.
func gameProductWithDates(id uuid.UUID, scalar time.Time, perRegion map[string]time.Time) enrichapi.Product {
	p := gameProduct(id)
	sc := openapi_types.Date{Time: scalar}
	p.Igdb.FirstReleaseDate = &sc
	if len(perRegion) > 0 {
		rows := make([]enrichapi.ReleaseDate, 0, len(perRegion))
		for region, when := range perRegion {
			rows = append(rows, enrichapi.ReleaseDate{Region: region, Date: openapi_types.Date{Time: when}})
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

	refs := []store.GameEntryRef{
		// stale stored date -> the ntsc_u chain hit (north_america) differs, must update.
		{EntryID: entry1, ProductID: productA, Region: "ntsc_u", FirstReleaseDate: ptr(time.Date(1990, time.January, 1, 0, 0, 0, 0, time.UTC))},
		// stored date already matches the pal chain hit (europe) -> no write.
		{EntryID: entry2, ProductID: productA, Region: "pal", FirstReleaseDate: ptr(euDate)},
		// region_free has no chain, falls back to the scalar; unset -> must update.
		{EntryID: entry3, ProductID: productB, Region: "region_free", FirstReleaseDate: nil},
	}

	var mu sync.Mutex
	productCalls := map[uuid.UUID]int{}
	updated := map[uuid.UUID]*time.Time{}

	st := &stubStore{
		listGameBackedRefs: func(context.Context) ([]store.GameEntryRef, error) { return refs, nil },
		setFirstReleaseDate: func(_ context.Context, entryID uuid.UUID, d *time.Time) error {
			mu.Lock()
			defer mu.Unlock()
			updated[entryID] = d
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
				return gameProductWithDates(id, time.Date(1998, time.January, 1, 0, 0, 0, 0, time.UTC),
					map[string]time.Time{"north_america": naDate, "europe": euDate}), nil
			case productB:
				return gameProductWithDates(id, scalarB, nil), nil
			default:
				t.Fatalf("unexpected product id %s", id)
				return enrichapi.Product{}, nil
			}
		},
	}
	srv, a := newUnitServer(t, st, enrich, newStubCache())

	resp := do(t, http.MethodPost, srv.URL+"/internal/resnapshot", a.token(t, uuid.NewString()), nil)
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
	if got.ProductsSeen != 2 || got.ProductsFailed != 0 || got.EntriesUpdated != 2 {
		t.Fatalf("counts: %+v", got)
	}
	if len(productCalls) != 2 || productCalls[productA] != 1 || productCalls[productB] != 1 {
		t.Fatalf("GetProduct must be called exactly once per distinct product: %v", productCalls)
	}
	if len(updated) != 2 {
		t.Fatalf("only changed rows must be written: %v", updated)
	}
	if d := updated[entry1]; d == nil || !d.Equal(naDate) {
		t.Fatalf("entry1 pick: %v", d)
	}
	if _, ok := updated[entry2]; ok {
		t.Fatal("entry2's pick is unchanged and must not be rewritten")
	}
	if d := updated[entry3]; d == nil || !d.Equal(scalarB) {
		t.Fatalf("entry3 pick: %v", d)
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
		setFirstReleaseDate: func(_ context.Context, entryID uuid.UUID, _ *time.Time) error {
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
	resp := do(t, http.MethodPost, srv.URL+"/internal/resnapshot", a.token(t, uuid.NewString()), nil)
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
		setFirstReleaseDate: func(_ context.Context, entryID uuid.UUID, d *time.Time) error {
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
	tok := a.token(t, uuid.NewString())

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

func TestCreateSubmission_GuardsCapsAndCreate(t *testing.T) {
	userID := uuid.New()
	entryID := uuid.New()
	customE := store.Entry{ID: entryID, UserID: userID, ItemType: "game", DisplayName: "Repro", Region: "pal"}
	productBacked := customE
	pid := uuid.New()
	productBacked.ProductID = &pid

	pendingCount, windowCount := int64(0), int64(0)
	var created *store.Submission
	st := &stubStore{
		getEntry: func(_ context.Context, u, id uuid.UUID) (store.Entry, error) {
			if u != userID || id != entryID {
				return store.Entry{}, store.ErrNotFound
			}
			return customE, nil
		},
		countPendingSubmissions: func(context.Context, uuid.UUID) (int64, error) { return pendingCount, nil },
		countSubmissionsSince:   func(context.Context, uuid.UUID, time.Time) (int64, error) { return windowCount, nil },
		createSubmission: func(_ context.Context, u, id uuid.UUID) (store.Submission, error) {
			s := store.Submission{ID: uuid.New(), EntryID: id, UserID: u, Status: "pending",
				CreatedAt: time.Now().UTC(), UpdatedAt: time.Now().UTC()}
			created = &s
			return s, nil
		},
	}
	srv, a := newUnitServer(t, st, &stubEnrichment{}, newStubCache())
	bearer := a.token(t, userID.String())

	// Foreign/missing entry -> 404 entry_not_found.
	resp := do(t, http.MethodPost, srv.URL+"/entries/"+uuid.NewString()+"/submission", bearer, nil)
	wantProblem(t, resp, http.StatusNotFound, "entry_not_found")

	// Product-backed -> 400 entry_not_custom.
	st.getEntry = func(context.Context, uuid.UUID, uuid.UUID) (store.Entry, error) { return productBacked, nil }
	resp = do(t, http.MethodPost, srv.URL+"/entries/"+entryID.String()+"/submission", bearer, nil)
	wantProblem(t, resp, http.StatusBadRequest, "entry_not_custom")
	st.getEntry = func(context.Context, uuid.UUID, uuid.UUID) (store.Entry, error) { return customE, nil }

	// Caps: pending first, then the rolling window.
	pendingCount = 10
	resp = do(t, http.MethodPost, srv.URL+"/entries/"+entryID.String()+"/submission", bearer, nil)
	wantProblem(t, resp, http.StatusTooManyRequests, "too_many_pending_submissions")
	pendingCount = 0
	windowCount = 20
	resp = do(t, http.MethodPost, srv.URL+"/entries/"+entryID.String()+"/submission", bearer, nil)
	wantProblem(t, resp, http.StatusTooManyRequests, "submission_rate_limited")
	windowCount = 0

	// The create.
	resp = do(t, http.MethodPost, srv.URL+"/entries/"+entryID.String()+"/submission", bearer, nil)
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("create: %d", resp.StatusCode)
	}
	if created == nil || created.EntryID != entryID {
		t.Fatalf("store create not called correctly: %+v", created)
	}

	// Double submit relays the store sentinel as 409.
	st.createSubmission = func(context.Context, uuid.UUID, uuid.UUID) (store.Submission, error) {
		return store.Submission{}, store.ErrSubmissionPending
	}
	resp = do(t, http.MethodPost, srv.URL+"/entries/"+entryID.String()+"/submission", bearer, nil)
	wantProblem(t, resp, http.StatusConflict, "submission_pending")
}

func TestSubmission_GetAndCancel(t *testing.T) {
	userID := uuid.New()
	entryID := uuid.New()
	reason := "duplicate of an existing product"
	st := &stubStore{
		getEntry: func(context.Context, uuid.UUID, uuid.UUID) (store.Entry, error) {
			return store.Entry{ID: entryID, UserID: userID}, nil
		},
		latestSubmissionForEntry: func(context.Context, uuid.UUID, uuid.UUID) (store.Submission, error) {
			return store.Submission{ID: uuid.New(), EntryID: entryID, UserID: userID,
				Status: "rejected", RejectReason: &reason,
				CreatedAt: time.Now().UTC(), UpdatedAt: time.Now().UTC()}, nil
		},
		cancelSubmission: func(context.Context, uuid.UUID, uuid.UUID) error { return store.ErrNotFound },
	}
	srv, a := newUnitServer(t, st, &stubEnrichment{}, newStubCache())
	bearer := a.token(t, userID.String())

	resp := do(t, http.MethodGet, srv.URL+"/entries/"+entryID.String()+"/submission", bearer, nil)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("get: %d", resp.StatusCode)
	}
	var sub api.Submission
	if err := json.NewDecoder(resp.Body).Decode(&sub); err != nil {
		t.Fatal(err)
	}
	if string(sub.Status) != "rejected" || sub.RejectReason == nil || *sub.RejectReason != reason {
		t.Fatalf("submission body wrong: %+v", sub)
	}

	resp = do(t, http.MethodDelete, srv.URL+"/entries/"+entryID.String()+"/submission", bearer, nil)
	wantProblem(t, resp, http.StatusNotFound, "submission_not_found")

	st.cancelSubmission = func(context.Context, uuid.UUID, uuid.UUID) error { return nil }
	resp = do(t, http.MethodDelete, srv.URL+"/entries/"+entryID.String()+"/submission", bearer, nil)
	if resp.StatusCode != http.StatusNoContent {
		t.Fatalf("cancel: %d", resp.StatusCode)
	}
}

func TestCreateEntry_CommunityProductSnapshotFallbacks(t *testing.T) {
	userID := uuid.New()
	productID := uuid.New()
	platName := "SNES"
	rd := openapi_types.Date{Time: time.Date(1995, 10, 9, 0, 0, 0, 0, time.UTC)}
	community := enrichapi.Product{
		Id: productID, Type: "game", Name: "Repro Alpha",
		Community: &enrichapi.CommunityMeta{PlatformName: &platName, FirstReleaseDate: &rd},
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

func TestSubmitVerdict_RejectApproveExistingAndGate(t *testing.T) {
	adminID := uuid.New()
	subID := uuid.New()
	userID := uuid.New()
	entryID := uuid.New()
	pending := store.Submission{ID: subID, EntryID: entryID, UserID: userID, Status: "pending",
		CreatedAt: time.Now().UTC(), UpdatedAt: time.Now().UTC()}
	productID := uuid.New()
	provider := enrichapi.Product{Id: productID, Type: "game", Name: "Chrono Trigger"}

	var approvedSnap *store.CatalogSnapshot
	st := &stubStore{
		getSubmission: func(context.Context, uuid.UUID) (store.Submission, error) { return pending, nil },
		getEntry: func(context.Context, uuid.UUID, uuid.UUID) (store.Entry, error) {
			return store.Entry{ID: entryID, UserID: userID, Region: "pal"}, nil
		},
		rejectSubmission: func(_ context.Context, id uuid.UUID, reason string) (store.Submission, error) {
			out := pending
			out.Status = "rejected"
			out.RejectReason = &reason
			return out, nil
		},
		approveSubmission: func(_ context.Context, id uuid.UUID, snap store.CatalogSnapshot) (store.Submission, error) {
			approvedSnap = &snap
			out := pending
			out.Status = "approved"
			out.ProductID = &snap.ProductID
			return out, nil
		},
	}
	enrich := &stubEnrichment{getProduct: func(context.Context, string, uuid.UUID) (enrichapi.Product, error) {
		return provider, nil
	}}
	srv, a := newUnitServer(t, st, enrich, newStubCache())
	admin := a.token(t, adminID.String(), "admin")

	// The gate: user role -> 403, store untouched (getSubmission nil
	// would panic - prove it is never reached with a fresh stub).
	gateSrv, ga := newUnitServer(t, &stubStore{}, &stubEnrichment{}, newStubCache())
	resp := do(t, http.MethodPost, gateSrv.URL+"/admin/submissions/"+subID.String()+"/verdict",
		ga.token(t, uuid.NewString()), jsonBody(map[string]any{"action": "reject", "reason": "x"}))
	wantProblem(t, resp, http.StatusForbidden, "forbidden")

	// reject requires a reason.
	resp = do(t, http.MethodPost, srv.URL+"/admin/submissions/"+subID.String()+"/verdict", admin,
		jsonBody(map[string]any{"action": "reject", "reason": "  "}))
	wantProblem(t, resp, http.StatusBadRequest, "invalid_body")

	resp = do(t, http.MethodPost, srv.URL+"/admin/submissions/"+subID.String()+"/verdict", admin,
		jsonBody(map[string]any{"action": "reject", "reason": "not a shared item"}))
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("reject: %d", resp.StatusCode)
	}

	// approve_existing validates the target then adopts with the
	// provider snapshot.
	resp = do(t, http.MethodPost, srv.URL+"/admin/submissions/"+subID.String()+"/verdict", admin,
		jsonBody(map[string]any{"action": "approve_existing", "product_id": productID.String()}))
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("approve_existing: %d", resp.StatusCode)
	}
	if approvedSnap == nil || approvedSnap.ProductID != productID || approvedSnap.DisplayName != "Chrono Trigger" {
		t.Fatalf("snapshot: %+v", approvedSnap)
	}

	// Unknown target -> 404 unknown_product.
	enrich.getProduct = func(context.Context, string, uuid.UUID) (enrichapi.Product, error) {
		return enrichapi.Product{}, enrichmentclient.ErrUnknownProduct
	}
	resp = do(t, http.MethodPost, srv.URL+"/admin/submissions/"+subID.String()+"/verdict", admin,
		jsonBody(map[string]any{"action": "approve_existing", "product_id": uuid.NewString()}))
	wantProblem(t, resp, http.StatusNotFound, "unknown_product")

	// A resolved row answers 409.
	st.getSubmission = func(context.Context, uuid.UUID) (store.Submission, error) {
		done := pending
		done.Status = "approved"
		return done, nil
	}
	resp = do(t, http.MethodPost, srv.URL+"/admin/submissions/"+subID.String()+"/verdict", admin,
		jsonBody(map[string]any{"action": "reject", "reason": "x"}))
	wantProblem(t, resp, http.StatusConflict, "submission_resolved")
}

func TestSubmitVerdict_ApproveNewMintRecordAdoptAndRetry(t *testing.T) {
	adminID := uuid.New()
	subID := uuid.New()
	userID := uuid.New()
	entryID := uuid.New()
	minted := uuid.New()
	pending := store.Submission{ID: subID, EntryID: entryID, UserID: userID, Status: "pending",
		CreatedAt: time.Now().UTC(), UpdatedAt: time.Now().UTC()}
	platName := "SNES"
	communityProduct := enrichapi.Product{Id: minted, Type: "game", Name: "Repro Alpha",
		Community: &enrichapi.CommunityMeta{PlatformName: &platName}}

	var mintedReq *enrichapi.CreateCommunityProductJSONRequestBody
	var recorded, adopted bool
	st := &stubStore{
		getSubmission: func(context.Context, uuid.UUID) (store.Submission, error) { return pending, nil },
		getEntry: func(context.Context, uuid.UUID, uuid.UUID) (store.Entry, error) {
			return store.Entry{ID: entryID, UserID: userID, Region: "pal"}, nil
		},
		recordSubmissionProduct: func(_ context.Context, id, productID uuid.UUID) error {
			if productID != minted {
				t.Fatalf("recorded %s, want %s", productID, minted)
			}
			recorded = true
			return nil
		},
		approveSubmission: func(_ context.Context, _ uuid.UUID, snap store.CatalogSnapshot) (store.Submission, error) {
			if snap.ProductID != minted || snap.PlatformName == nil || *snap.PlatformName != "SNES" {
				t.Fatalf("adopt snapshot: %+v", snap)
			}
			adopted = true
			out := pending
			out.Status = "approved"
			out.ProductID = &snap.ProductID
			return out, nil
		},
	}
	enrich := &stubEnrichment{
		createCommunityProduct: func(_ context.Context, _ string, req enrichapi.CreateCommunityProductJSONRequestBody) (enrichapi.Product, error) {
			mintedReq = &req
			return communityProduct, nil
		},
		getProduct: func(context.Context, string, uuid.UUID) (enrichapi.Product, error) {
			return communityProduct, nil
		},
	}
	srv, a := newUnitServer(t, st, enrich, newStubCache())
	admin := a.token(t, adminID.String(), "admin")

	body := map[string]any{"action": "approve_new", "product": map[string]any{
		"type": "game", "name": "Repro Alpha", "platform_name": "SNES", "edition": "glow cart",
	}}
	resp := do(t, http.MethodPost, srv.URL+"/admin/submissions/"+subID.String()+"/verdict", admin, jsonBody(body))
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("approve_new: %d", resp.StatusCode)
	}
	if mintedReq == nil || mintedReq.Name != "Repro Alpha" || mintedReq.Edition == nil || *mintedReq.Edition != "glow cart" {
		t.Fatalf("mint request: %+v", mintedReq)
	}
	if !recorded || !adopted {
		t.Fatalf("phases: recorded=%v adopted=%v", recorded, adopted)
	}

	// Retry with a recorded id: the mint must NOT run again (nil
	// field would panic), adoption completes.
	adopted = false
	withRecord := pending
	withRecord.ProductID = &minted
	st.getSubmission = func(context.Context, uuid.UUID) (store.Submission, error) { return withRecord, nil }
	enrich.createCommunityProduct = nil
	resp = do(t, http.MethodPost, srv.URL+"/admin/submissions/"+subID.String()+"/verdict", admin, jsonBody(body))
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("retry: %d", resp.StatusCode)
	}
	if !adopted {
		t.Fatal("retry must adopt from the recorded id")
	}

	// Mint failure: 502, nothing recorded, row untouched.
	st.getSubmission = func(context.Context, uuid.UUID) (store.Submission, error) { return pending, nil }
	enrich.createCommunityProduct = func(context.Context, string, enrichapi.CreateCommunityProductJSONRequestBody) (enrichapi.Product, error) {
		return enrichapi.Product{}, enrichmentclient.ErrUnavailable
	}
	st.recordSubmissionProduct = nil // reaching it would panic
	resp = do(t, http.MethodPost, srv.URL+"/admin/submissions/"+subID.String()+"/verdict", admin, jsonBody(body))
	wantProblem(t, resp, http.StatusBadGateway, "enrichment_unavailable")
}

// TestSubmitVerdict_ApproveEntryVanishedMidRace guards the adoption
// tail's GetEntry call against the cascade micro-race: if the
// submission's entry is deleted between GetSubmission and here (the
// cascade also removes the submission row, but a verdict already in
// flight holds its own copy), the caller must get a truthful 404, not
// a mystery 500. approveSubmission stays nil - reaching it would
// panic, proving the handler returns before ever calling it.
func TestSubmitVerdict_ApproveEntryVanishedMidRace(t *testing.T) {
	adminID := uuid.New()
	subID := uuid.New()
	userID := uuid.New()
	entryID := uuid.New()
	pending := store.Submission{ID: subID, EntryID: entryID, UserID: userID, Status: "pending",
		CreatedAt: time.Now().UTC(), UpdatedAt: time.Now().UTC()}
	productID := uuid.New()
	provider := enrichapi.Product{Id: productID, Type: "game", Name: "Chrono Trigger"}

	st := &stubStore{
		getSubmission: func(context.Context, uuid.UUID) (store.Submission, error) { return pending, nil },
		getEntry: func(context.Context, uuid.UUID, uuid.UUID) (store.Entry, error) {
			return store.Entry{}, store.ErrNotFound
		},
	}
	enrich := &stubEnrichment{getProduct: func(context.Context, string, uuid.UUID) (enrichapi.Product, error) {
		return provider, nil
	}}
	srv, a := newUnitServer(t, st, enrich, newStubCache())
	admin := a.token(t, adminID.String(), "admin")

	resp := do(t, http.MethodPost, srv.URL+"/admin/submissions/"+subID.String()+"/verdict", admin,
		jsonBody(map[string]any{"action": "approve_existing", "product_id": productID.String()}))
	wantProblem(t, resp, http.StatusNotFound, "entry_not_found")
}

func TestListSubmissions_GateAndEnvelope(t *testing.T) {
	rowUser := uuid.New()
	plat := "SNES"
	st := &stubStore{listPendingSubmissions: func(_ context.Context, limit, offset int) ([]store.SubmissionProposal, int64, error) {
		if limit != 200 || offset != 0 {
			t.Fatalf("defaults: limit=%d offset=%d", limit, offset)
		}
		return []store.SubmissionProposal{{
			Submission: store.Submission{ID: uuid.New(), EntryID: uuid.New(), UserID: rowUser,
				Status: "pending", CreatedAt: time.Now().UTC(), UpdatedAt: time.Now().UTC()},
			DisplayName: "Repro Alpha", ItemType: "game", PlatformName: &plat, Region: "pal",
		}}, 1, nil
	}}
	srv, a := newUnitServer(t, st, &stubEnrichment{}, newStubCache())

	resp := do(t, http.MethodGet, srv.URL+"/admin/submissions", a.token(t, uuid.NewString()), nil)
	wantProblem(t, resp, http.StatusForbidden, "forbidden")

	resp = do(t, http.MethodGet, srv.URL+"/admin/submissions", a.token(t, uuid.NewString(), "admin"), nil)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("list: %d", resp.StatusCode)
	}
	var page api.AdminSubmissionsPage
	if err := json.NewDecoder(resp.Body).Decode(&page); err != nil {
		t.Fatal(err)
	}
	if page.TotalCount != 1 || len(page.Submissions) != 1 {
		t.Fatalf("envelope: %+v", page)
	}
	row := page.Submissions[0]
	if row.UserId != rowUser || row.DisplayName != "Repro Alpha" || string(row.Region) != "pal" {
		t.Fatalf("row: %+v", row)
	}
}

func TestSubmissionAck_OwnershipApprovedAndIdempotent(t *testing.T) {
	userID := uuid.New()
	entryID := uuid.New()
	approved := store.Submission{ID: uuid.New(), EntryID: entryID, UserID: userID, Status: "approved"}
	var stamped int
	st := &stubStore{
		getEntry: func(context.Context, uuid.UUID, uuid.UUID) (store.Entry, error) {
			return store.Entry{ID: entryID, UserID: userID}, nil
		},
		latestApprovedSubmissionForEntry: func(context.Context, uuid.UUID, uuid.UUID) (store.Submission, error) { return approved, nil },
		ackSubmissionResolution:          func(context.Context, uuid.UUID) error { stamped++; return nil },
	}
	srv, a := newUnitServer(t, st, &stubEnrichment{}, newStubCache())
	bearer := a.token(t, userID.String())

	resp := do(t, http.MethodPost, srv.URL+"/entries/"+entryID.String()+"/submission/ack", bearer, nil)
	if resp.StatusCode != http.StatusNoContent {
		t.Fatalf("ack: %d", resp.StatusCode)
	}
	if stamped != 1 {
		t.Fatalf("stamp calls = %d, want 1", stamped)
	}

	// No approved submission -> 404 submission_not_found.
	st.latestApprovedSubmissionForEntry = func(context.Context, uuid.UUID, uuid.UUID) (store.Submission, error) {
		return store.Submission{}, store.ErrNotFound
	}
	resp = do(t, http.MethodPost, srv.URL+"/entries/"+entryID.String()+"/submission/ack", bearer, nil)
	wantProblem(t, resp, http.StatusNotFound, "submission_not_found")

	// Foreign/missing entry -> 404 entry_not_found.
	st.getEntry = func(context.Context, uuid.UUID, uuid.UUID) (store.Entry, error) {
		return store.Entry{}, store.ErrNotFound
	}
	resp = do(t, http.MethodPost, srv.URL+"/entries/"+entryID.String()+"/submission/ack", bearer, nil)
	wantProblem(t, resp, http.StatusNotFound, "entry_not_found")

	// Already acked -> 204 with no stamp call.
	already := time.Now().UTC()
	st.getEntry = func(context.Context, uuid.UUID, uuid.UUID) (store.Entry, error) {
		return store.Entry{ID: entryID, UserID: userID}, nil
	}
	st.latestApprovedSubmissionForEntry = func(context.Context, uuid.UUID, uuid.UUID) (store.Submission, error) {
		return store.Submission{ID: approved.ID, Status: "approved", ResolutionAckAt: &already}, nil
	}
	before := stamped
	resp = do(t, http.MethodPost, srv.URL+"/entries/"+entryID.String()+"/submission/ack", bearer, nil)
	if resp.StatusCode != http.StatusNoContent || stamped != before {
		t.Fatalf("already-acked: %d, stamp calls %d->%d", resp.StatusCode, before, stamped)
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

func TestApproveNew_ForwardsCoverToMint(t *testing.T) {
	adminID := uuid.New()
	userID := uuid.New()
	entryID := uuid.New()
	subID := uuid.New()
	var mintBody enrichapi.CreateCommunityProductJSONRequestBody
	st := &stubStore{
		getSubmission: func(context.Context, uuid.UUID) (store.Submission, error) {
			return store.Submission{ID: subID, EntryID: entryID, UserID: userID, Status: "pending"}, nil
		},
		recordSubmissionProduct: func(context.Context, uuid.UUID, uuid.UUID) error { return nil },
		approveSubmission: func(_ context.Context, _ uuid.UUID, _ store.CatalogSnapshot) (store.Submission, error) {
			return store.Submission{ID: subID, EntryID: entryID, Status: "approved"}, nil
		},
		getEntry: func(context.Context, uuid.UUID, uuid.UUID) (store.Entry, error) {
			return store.Entry{ID: entryID, UserID: userID, Region: "pal"}, nil
		},
	}
	minted := uuid.New()
	enr := &stubEnrichment{
		createCommunityProduct: func(_ context.Context, _ string, req enrichapi.CreateCommunityProductJSONRequestBody) (enrichapi.Product, error) {
			mintBody = req
			cu := "https://img.example/sub.jpg"
			return enrichapi.Product{Id: minted, Type: "game", Name: "Repro", Community: &enrichapi.CommunityMeta{CoverUrl: &cu}}, nil
		},
		getProduct: func(_ context.Context, _ string, id uuid.UUID) (enrichapi.Product, error) {
			cu := "https://img.example/sub.jpg"
			return enrichapi.Product{Id: id, Type: "game", Name: "Repro", Community: &enrichapi.CommunityMeta{CoverUrl: &cu}}, nil
		},
	}
	srv, a := newUnitServer(t, st, enr, newStubCache())
	bearer := a.token(t, adminID.String(), "admin")

	body := `{"action":"approve_new","product":{"type":"game","name":"Repro","cover_url":"https://img.example/sub.jpg"}}`
	resp := do(t, http.MethodPost, srv.URL+"/admin/submissions/"+subID.String()+"/verdict", bearer, strings.NewReader(body))
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("approve_new: %d", resp.StatusCode)
	}
	if mintBody.CoverUrl == nil || *mintBody.CoverUrl != "https://img.example/sub.jpg" {
		t.Fatalf("cover not forwarded to mint: %+v", mintBody.CoverUrl)
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
