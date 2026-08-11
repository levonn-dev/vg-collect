package server_test

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	openapi_types "github.com/oapi-codegen/runtime/types"
	"github.com/testcontainers/testcontainers-go"
	tcpostgres "github.com/testcontainers/testcontainers-go/modules/postgres"
	tcvalkey "github.com/testcontainers/testcontainers-go/modules/valkey"
	"github.com/testcontainers/testcontainers-go/wait"

	"github.com/levonn-dev/vgkeep/libs/go/pgkit"
	"github.com/levonn-dev/vgkeep/libs/go/valkeykit"
	"github.com/levonn-dev/vgkeep/services/collection/internal/cache"
	"github.com/levonn-dev/vgkeep/services/collection/internal/enrichmentclient"
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
	ackRegionMismatch func(ctx context.Context, userID, entryID uuid.UUID) error
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
	setSnapshotFields     func(ctx context.Context, entryID uuid.UUID, d *time.Time, name, translit, cover *string, developers, publishers []string) error
	countEntriesByProduct func(ctx context.Context, productID uuid.UUID) (int64, error)

	listAutoGameRematchRefs func(ctx context.Context) ([]store.RematchEntryRef, error)
	repointEntry            func(ctx context.Context, entryID, productID uuid.UUID, d *time.Time, name, translit, cover *string, developers, publishers []string) error

	listNameOnlyPlatformEntries func(ctx context.Context) ([]store.PlatformEntryRef, error)
	setEntryPlatformIdentity    func(ctx context.Context, entryID uuid.UUID, igdbID int64, name string) error

	listOpenRegionEntries      func(ctx context.Context, known []string) ([]store.OpenRegionEntryRef, error)
	promoteEntryRegion         func(ctx context.Context, entryID uuid.UUID, region string) error
	promoteEntryRegionSnapshot func(ctx context.Context, entryID uuid.UUID, region string, d *time.Time, name, translit, cover *string) error

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

func (s *stubStore) AckRegionMismatch(ctx context.Context, userID, entryID uuid.UUID) error {
	if s.ackRegionMismatch == nil {
		panic("unexpected AckRegionMismatch")
	}
	return s.ackRegionMismatch(ctx, userID, entryID)
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

func (s *stubStore) SetSnapshotFields(ctx context.Context, entryID uuid.UUID, d *time.Time, name, translit, cover *string, developers, publishers []string) error {
	if s.setSnapshotFields == nil {
		panic("unexpected SetSnapshotFields")
	}
	return s.setSnapshotFields(ctx, entryID, d, name, translit, cover, developers, publishers)
}

func (s *stubStore) ListAutoGameRematchRefs(ctx context.Context) ([]store.RematchEntryRef, error) {
	if s.listAutoGameRematchRefs == nil {
		panic("unexpected ListAutoGameRematchRefs")
	}
	return s.listAutoGameRematchRefs(ctx)
}

func (s *stubStore) RepointEntry(ctx context.Context, entryID, productID uuid.UUID, d *time.Time, name, translit, cover *string, developers, publishers []string) error {
	if s.repointEntry == nil {
		panic("unexpected RepointEntry")
	}
	return s.repointEntry(ctx, entryID, productID, d, name, translit, cover, developers, publishers)
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

func (s *stubStore) ListOpenRegionEntries(ctx context.Context, known []string) ([]store.OpenRegionEntryRef, error) {
	if s.listOpenRegionEntries == nil {
		panic("unexpected ListOpenRegionEntries")
	}
	return s.listOpenRegionEntries(ctx, known)
}

func (s *stubStore) PromoteEntryRegion(ctx context.Context, entryID uuid.UUID, region string) error {
	if s.promoteEntryRegion == nil {
		panic("unexpected PromoteEntryRegion")
	}
	return s.promoteEntryRegion(ctx, entryID, region)
}

func (s *stubStore) PromoteEntryRegionSnapshot(ctx context.Context, entryID uuid.UUID, region string, d *time.Time, name, translit, cover *string) error {
	if s.promoteEntryRegionSnapshot == nil {
		panic("unexpected PromoteEntryRegionSnapshot")
	}
	return s.promoteEntryRegionSnapshot(ctx, entryID, region, d, name, translit, cover)
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
	resolve                func(ctx context.Context, bearer string, req enrichapi.ResolveRequest) (enrichapi.Product, error)
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

func (s *stubEnrichment) Resolve(ctx context.Context, bearer string, req enrichapi.ResolveRequest) (enrichapi.Product, error) {
	if s.resolve == nil {
		panic("unexpected Resolve")
	}
	return s.resolve(ctx, bearer, req)
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

func jsonBody(m map[string]any) *bytes.Reader {
	b, _ := json.Marshal(m)
	return bytes.NewReader(b)
}

// gameProduct is a canonical enrichment product for snapshot tests.
func gameProduct(id uuid.UUID) enrichapi.Product {
	released := openapi_types.Date{Time: time.Date(1995, time.March, 11, 0, 0, 0, 0, time.UTC)}
	return enrichapi.Product{
		Id:   id,
		Type: "game",
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

// pricedGameProduct is gameProduct plus a PriceCharting mapping under
// consoleName, for the region-repoint arm's console-class guard.
func pricedGameProduct(id uuid.UUID, consoleName string) enrichapi.Product {
	p := gameProduct(id)
	p.Pricecharting = &enrichapi.PricechartingMeta{ConsoleName: consoleName, PcProductId: 5000}
	return p
}

// localizedGameProduct is gameProduct plus per-region presentation
// bundles: a full ja-JP one (native-script title, transliteration,
// regional box art), a cover-only EU one, and a name-only ko-KR one -
// the sparse shapes the provider actually serves.
func localizedGameProduct(id uuid.UUID) enrichapi.Product {
	p := gameProduct(id)
	p.Igdb.Localizations = &[]enrichapi.Localization{
		{
			Region:   "ja-JP",
			Name:     new("聖剣伝説3"),
			Translit: new("Seiken Densetsu 3"),
			CoverUrl: new("https://images.igdb.example/jp.jpg"),
		},
		{Region: "EU", CoverUrl: new("https://images.igdb.example/eu.jpg")},
		{Region: "ko-KR", Name: new("성검전설 3")},
	}
	return p
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

// One Postgres container and one Valkey container serve this whole
// package. The per-test containers this replaces spent most of the
// package's runtime on boots, and that churn was the bulk of the
// Docker-daemon load behind the WSL2 connection-refused flakes. Each
// test still gets exactly what the old fixture gave it - a freshly
// migrated database and an empty cache - via the drop-schema +
// re-migrate and FlushAll resets in newStack. No Terminate: the
// testcontainers reaper collects the containers when the test process
// exits.
var sharedPG struct {
	once sync.Once
	url  string
	err  error
}

var sharedVK struct {
	once sync.Once
	url  string
	err  error
}

func newStack(t *testing.T) *stack {
	t.Helper()
	if testing.Short() {
		t.Skip("requires docker")
	}
	ctx := context.Background()

	sharedPG.once.Do(func() {
		pg, err := tcpostgres.Run(ctx, "postgres:17-alpine",
			tcpostgres.WithDatabase("collection"), tcpostgres.WithUsername("c"), tcpostgres.WithPassword("p"),
			testcontainers.WithWaitStrategy(
				wait.ForLog("database system is ready to accept connections").
					WithOccurrence(2).WithStartupTimeout(60*time.Second),
				wait.ForListeningPort("5432/tcp")))
		if err != nil {
			sharedPG.err = err
			return
		}
		sharedPG.url, sharedPG.err = pg.ConnectionString(ctx, "sslmode=disable")
	})
	if sharedPG.err != nil {
		t.Fatal(sharedPG.err)
	}
	// Reset: drop everything the previous test left (schema_migrations
	// included) and re-run the embedded migrations, so each test opens
	// on a fresh, fully migrated database - migration-seeded rows and
	// all. Two Execs because pgx's extended protocol takes one
	// statement at a time.
	conn, err := pgx.Connect(ctx, sharedPG.url)
	if err != nil {
		t.Fatal(err)
	}
	for _, stmt := range []string{"DROP SCHEMA public CASCADE", "CREATE SCHEMA public"} {
		if _, err := conn.Exec(ctx, stmt); err != nil {
			_ = conn.Close(ctx)
			t.Fatal(err)
		}
	}
	_ = conn.Close(ctx)
	if err := pgkit.Migrate(sharedPG.url, migrations.FS, "."); err != nil {
		t.Fatal(err)
	}
	pool, err := pgkit.Connect(ctx, sharedPG.url)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(pool.Close)

	sharedVK.once.Do(func() {
		vk, err := tcvalkey.Run(ctx, "valkey/valkey:8-alpine")
		if err != nil {
			sharedVK.err = err
			return
		}
		sharedVK.url, sharedVK.err = vk.ConnectionString(ctx)
	})
	if sharedVK.err != nil {
		t.Fatal(sharedVK.err)
	}
	rdb, err := valkeykit.Connect(ctx, sharedVK.url)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = rdb.Close() })
	// Reset: flush whatever the previous test cached so each test
	// starts on an empty cache.
	if err := rdb.FlushAll(ctx).Err(); err != nil {
		t.Fatal(err)
	}

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

func dashboardStore(_ uuid.UUID, rows []store.PricingRow) *stubStore {
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

// waitFor polls until check passes (the entry rematch detaches, same
// as the catalog refresh it mirrors).
func waitFor(t *testing.T, timeout time.Duration, check func() bool) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if check() {
			return
		}
		time.Sleep(25 * time.Millisecond)
	}
	t.Fatal("condition not reached in time")
}
