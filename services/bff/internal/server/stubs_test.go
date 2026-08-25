// Shared test doubles for AuthAPI, UserAPI, EnrichmentAPI, and CollectionAPI:
// one hand-rolled, function-field stub per interface, used by every *_test.go
// here. An unset method field panics naming the stub and method, never a silent zero value.
package server

import (
	"context"
	"sync"

	"github.com/google/uuid"

	"github.com/levonn-dev/vgkeep/libs/go/contract/collectionapi"
	"github.com/levonn-dev/vgkeep/libs/go/contract/common"
	"github.com/levonn-dev/vgkeep/libs/go/contract/enrichapi"
	"github.com/levonn-dev/vgkeep/libs/go/contract/userapi"
	"github.com/levonn-dev/vgkeep/services/bff/internal/authclient"
	"github.com/levonn-dev/vgkeep/services/bff/internal/collectionclient"
	"github.com/levonn-dev/vgkeep/services/bff/internal/enrichmentclient"
	"github.com/levonn-dev/vgkeep/services/bff/internal/userclient"
)

// stubAuth implements AuthAPI via function fields.
type stubAuth struct {
	start          func(ctx context.Context, provider string) (string, error)
	callback       func(ctx context.Context, code, state string) (authclient.TokenPair, error)
	dev            func(ctx context.Context, user string) (authclient.TokenPair, error)
	refresh        func(ctx context.Context, rt string) (authclient.TokenPair, error)
	revoke         func(ctx context.Context, rt string) error
	providers      func(ctx context.Context) ([]string, error)
	linkStart      func(ctx context.Context, provider, bearer string) (string, error)
	devLink        func(ctx context.Context, user, bearer string) (authclient.TokenPair, error)
	listIdentities func(ctx context.Context, userID, bearer string) ([]common.Identity, error)
	deleteIdentity func(ctx context.Context, identityID uuid.UUID, bearer string) error
	deleteUserAuth func(ctx context.Context, userID, bearer string) error
}

func (s *stubAuth) Start(ctx context.Context, p string) (string, error) {
	if s.start == nil {
		panic("stubAuth: unexpected Start")
	}
	return s.start(ctx, p)
}

func (s *stubAuth) Callback(ctx context.Context, c, st string) (authclient.TokenPair, error) {
	if s.callback == nil {
		panic("stubAuth: unexpected Callback")
	}
	return s.callback(ctx, c, st)
}

func (s *stubAuth) DevToken(ctx context.Context, u string) (authclient.TokenPair, error) {
	if s.dev == nil {
		panic("stubAuth: unexpected DevToken")
	}
	return s.dev(ctx, u)
}

func (s *stubAuth) Refresh(ctx context.Context, rt string) (authclient.TokenPair, error) {
	if s.refresh == nil {
		panic("stubAuth: unexpected Refresh")
	}
	return s.refresh(ctx, rt)
}

func (s *stubAuth) Revoke(ctx context.Context, rt string) error {
	if s.revoke == nil {
		panic("stubAuth: unexpected Revoke")
	}
	return s.revoke(ctx, rt)
}

func (s *stubAuth) Providers(ctx context.Context) ([]string, error) {
	if s.providers == nil {
		panic("stubAuth: unexpected Providers")
	}
	return s.providers(ctx)
}

func (s *stubAuth) LinkStart(ctx context.Context, provider, bearer string) (string, error) {
	if s.linkStart == nil {
		panic("stubAuth: unexpected LinkStart")
	}
	return s.linkStart(ctx, provider, bearer)
}

func (s *stubAuth) DevLink(ctx context.Context, user, bearer string) (authclient.TokenPair, error) {
	if s.devLink == nil {
		panic("stubAuth: unexpected DevLink")
	}
	return s.devLink(ctx, user, bearer)
}

func (s *stubAuth) ListIdentities(ctx context.Context, userID, bearer string) ([]common.Identity, error) {
	if s.listIdentities == nil {
		panic("stubAuth: unexpected ListIdentities")
	}
	return s.listIdentities(ctx, userID, bearer)
}

func (s *stubAuth) DeleteIdentity(ctx context.Context, identityID uuid.UUID, bearer string) error {
	if s.deleteIdentity == nil {
		panic("stubAuth: unexpected DeleteIdentity")
	}
	return s.deleteIdentity(ctx, identityID, bearer)
}

func (s *stubAuth) DeleteUserAuth(ctx context.Context, userID, bearer string) error {
	if s.deleteUserAuth == nil {
		panic("stubAuth: unexpected DeleteUserAuth")
	}
	return s.deleteUserAuth(ctx, userID, bearer)
}

var _ AuthAPI = (*stubAuth)(nil)

// stubUsers implements UserAPI via function fields.
type stubUsers struct {
	get    func(ctx context.Context, id, bearer string) (userapi.User, error)
	update func(ctx context.Context, id, bearer string, body []byte) (userclient.Result, error)
	delete func(ctx context.Context, id, bearer string) error

	sharedProfile    func(ctx context.Context, bearer, handle string) (userapi.ProfileCard, error)
	sharedCardsByIDs func(ctx context.Context, bearer string, ids []uuid.UUID) ([]userapi.ProfileCard, error)
	searchProfiles   func(ctx context.Context, bearer, q string) (userclient.Result, error)
}

func (s *stubUsers) Get(ctx context.Context, id, bearer string) (userapi.User, error) {
	if s.get == nil {
		panic("stubUsers: unexpected Get")
	}
	return s.get(ctx, id, bearer)
}

func (s *stubUsers) Update(ctx context.Context, id, bearer string, body []byte) (userclient.Result, error) {
	if s.update == nil {
		panic("stubUsers: unexpected Update")
	}
	return s.update(ctx, id, bearer, body)
}

func (s *stubUsers) Delete(ctx context.Context, id, bearer string) error {
	if s.delete == nil {
		panic("stubUsers: unexpected Delete")
	}
	return s.delete(ctx, id, bearer)
}

func (s *stubUsers) SharedProfile(ctx context.Context, bearer, handle string) (userapi.ProfileCard, error) {
	if s.sharedProfile == nil {
		panic("stubUsers: unexpected SharedProfile")
	}
	return s.sharedProfile(ctx, bearer, handle)
}

func (s *stubUsers) SharedCardsByIDs(ctx context.Context, bearer string, ids []uuid.UUID) ([]userapi.ProfileCard, error) {
	if s.sharedCardsByIDs == nil {
		panic("stubUsers: unexpected SharedCardsByIDs")
	}
	return s.sharedCardsByIDs(ctx, bearer, ids)
}

func (s *stubUsers) SearchProfiles(ctx context.Context, bearer, q string) (userclient.Result, error) {
	if s.searchProfiles == nil {
		panic("stubUsers: unexpected SearchProfiles")
	}
	return s.searchProfiles(ctx, bearer, q)
}

var _ UserAPI = (*stubUsers)(nil)

// stubEnrichment implements EnrichmentAPI via function fields; mu/calls/
// gotAuth/scoreCalls are opt-in bookkeeping, meaningful only for callers whose closures update them.
type stubEnrichment struct {
	search  func(ctx context.Context, bearer, typ, q string) (enrichmentclient.Result, error)
	resolve func(ctx context.Context, bearer string, body []byte) (enrichmentclient.Result, error)
	product func(ctx context.Context, bearer string, id uuid.UUID) (enrichmentclient.Result, error)
	score   func(ctx context.Context, bearer string, req enrichapi.ScoreRequest) ([]byte, bool, error)
	fx      func(ctx context.Context, bearer string) (enrichmentclient.Result, error)

	listPlatforms func(ctx context.Context, bearer string) (enrichmentclient.Result, error)

	unmatchedProducts func(ctx context.Context, bearer string, params *enrichapi.ListUnmatchedProductsParams) (enrichmentclient.Result, error)
	communityProducts func(ctx context.Context, bearer string, params *enrichapi.ListCommunityProductsParams) (enrichmentclient.Result, error)
	setProductMapping func(ctx context.Context, bearer string, id uuid.UUID, body []byte) (enrichmentclient.Result, error)
	triggerRefresh    func(ctx context.Context, bearer string) (enrichmentclient.Result, error)
	deleteProduct     func(ctx context.Context, bearer string, id uuid.UUID) (enrichmentclient.Result, error)

	createCommunityProduct  func(ctx context.Context, bearer string, body []byte) (enrichmentclient.Result, error)
	promoteProduct          func(ctx context.Context, bearer string, id uuid.UUID, body []byte) (enrichmentclient.Result, error)
	promoteCandidates       func(ctx context.Context, bearer string, params *enrichapi.ListPromoteCandidatesParams) (enrichmentclient.Result, error)
	dismissPromoteCandidate func(ctx context.Context, bearer string, id uuid.UUID, body []byte) (enrichmentclient.Result, error)

	normalizeCommunityRegions func(ctx context.Context, bearer string) (enrichmentclient.Result, error)

	mu         sync.Mutex
	calls      int
	gotAuth    []string
	scoreCalls int
}

func (s *stubEnrichment) Search(ctx context.Context, bearer, typ, q string) (enrichmentclient.Result, error) {
	if s.search == nil {
		panic("stubEnrichment: unexpected Search")
	}
	return s.search(ctx, bearer, typ, q)
}

func (s *stubEnrichment) Resolve(ctx context.Context, bearer string, body []byte) (enrichmentclient.Result, error) {
	if s.resolve == nil {
		panic("stubEnrichment: unexpected Resolve")
	}
	return s.resolve(ctx, bearer, body)
}

func (s *stubEnrichment) Product(ctx context.Context, bearer string, id uuid.UUID) (enrichmentclient.Result, error) {
	if s.product == nil {
		panic("stubEnrichment: unexpected Product")
	}
	return s.product(ctx, bearer, id)
}

func (s *stubEnrichment) Score(ctx context.Context, bearer string, req enrichapi.ScoreRequest) ([]byte, bool, error) {
	if s.score == nil {
		panic("stubEnrichment: unexpected Score")
	}
	return s.score(ctx, bearer, req)
}

func (s *stubEnrichment) FX(ctx context.Context, bearer string) (enrichmentclient.Result, error) {
	if s.fx == nil {
		panic("stubEnrichment: unexpected FX")
	}
	return s.fx(ctx, bearer)
}

func (s *stubEnrichment) ListPlatforms(ctx context.Context, bearer string) (enrichmentclient.Result, error) {
	if s.listPlatforms == nil {
		panic("stubEnrichment: unexpected ListPlatforms")
	}
	return s.listPlatforms(ctx, bearer)
}

func (s *stubEnrichment) UnmatchedProducts(ctx context.Context, bearer string, params *enrichapi.ListUnmatchedProductsParams) (enrichmentclient.Result, error) {
	if s.unmatchedProducts == nil {
		panic("stubEnrichment: unexpected UnmatchedProducts")
	}
	return s.unmatchedProducts(ctx, bearer, params)
}

func (s *stubEnrichment) CommunityProducts(ctx context.Context, bearer string, params *enrichapi.ListCommunityProductsParams) (enrichmentclient.Result, error) {
	if s.communityProducts == nil {
		panic("stubEnrichment: unexpected CommunityProducts")
	}
	return s.communityProducts(ctx, bearer, params)
}

func (s *stubEnrichment) SetProductMapping(ctx context.Context, bearer string, id uuid.UUID, body []byte) (enrichmentclient.Result, error) {
	if s.setProductMapping == nil {
		panic("stubEnrichment: unexpected SetProductMapping")
	}
	return s.setProductMapping(ctx, bearer, id, body)
}

func (s *stubEnrichment) TriggerRefresh(ctx context.Context, bearer string) (enrichmentclient.Result, error) {
	if s.triggerRefresh == nil {
		panic("stubEnrichment: unexpected TriggerRefresh")
	}
	return s.triggerRefresh(ctx, bearer)
}

func (s *stubEnrichment) DeleteProduct(ctx context.Context, bearer string, id uuid.UUID) (enrichmentclient.Result, error) {
	if s.deleteProduct == nil {
		panic("stubEnrichment: unexpected DeleteProduct")
	}
	return s.deleteProduct(ctx, bearer, id)
}

func (s *stubEnrichment) CreateCommunityProduct(ctx context.Context, bearer string, body []byte) (enrichmentclient.Result, error) {
	if s.createCommunityProduct == nil {
		panic("stubEnrichment: unexpected CreateCommunityProduct")
	}
	return s.createCommunityProduct(ctx, bearer, body)
}

func (s *stubEnrichment) PromoteProduct(ctx context.Context, bearer string, id uuid.UUID, body []byte) (enrichmentclient.Result, error) {
	if s.promoteProduct == nil {
		panic("stubEnrichment: unexpected PromoteProduct")
	}
	return s.promoteProduct(ctx, bearer, id, body)
}

func (s *stubEnrichment) PromoteCandidates(ctx context.Context, bearer string, params *enrichapi.ListPromoteCandidatesParams) (enrichmentclient.Result, error) {
	if s.promoteCandidates == nil {
		panic("stubEnrichment: unexpected PromoteCandidates")
	}
	return s.promoteCandidates(ctx, bearer, params)
}

func (s *stubEnrichment) DismissPromoteCandidate(ctx context.Context, bearer string, id uuid.UUID, body []byte) (enrichmentclient.Result, error) {
	if s.dismissPromoteCandidate == nil {
		panic("stubEnrichment: unexpected DismissPromoteCandidate")
	}
	return s.dismissPromoteCandidate(ctx, bearer, id, body)
}

func (s *stubEnrichment) NormalizeCommunityRegions(ctx context.Context, bearer string) (enrichmentclient.Result, error) {
	if s.normalizeCommunityRegions == nil {
		panic("stubEnrichment: unexpected NormalizeCommunityRegions")
	}
	return s.normalizeCommunityRegions(ctx, bearer)
}

// callCount reports how many times a caller's search closure tallied itself (see the type comment).
func (s *stubEnrichment) callCount() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.calls
}

// scoreCallCount mirrors callCount for score calls.
func (s *stubEnrichment) scoreCallCount() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.scoreCalls
}

var _ EnrichmentAPI = (*stubEnrichment)(nil)

// stubCollection implements CollectionAPI via function fields. Most
// methods relay through call, tagging op/bearer (gotOps/gotBearer/gotAuth)
// so tests assert call counts without a field per method; shared-page and submission surfaces use dedicated fields instead.
type stubCollection struct {
	answer  func(op string) (collectionclient.Result, error)
	library func(ctx context.Context, bearer string) (collectionapi.LibrarySummary, error)

	createSubmission   func(ctx context.Context, bearer string, id uuid.UUID) (collectionclient.Result, error)
	getSubmission      func(ctx context.Context, bearer string, id uuid.UUID) (collectionclient.Result, error)
	cancelSubmission   func(ctx context.Context, bearer string, id uuid.UUID) (collectionclient.Result, error)
	ackSubmission      func(ctx context.Context, bearer string, id uuid.UUID) (collectionclient.Result, error)
	listSubmissions    func(ctx context.Context, bearer string, params *collectionapi.ListSubmissionsParams) (collectionclient.Result, error)
	submitVerdict      func(ctx context.Context, bearer string, id uuid.UUID, body []byte) (collectionclient.Result, error)
	triggerRematch     func(ctx context.Context, bearer string) (collectionclient.Result, error)
	resnapshot         func(ctx context.Context, bearer string) (collectionclient.Result, error)
	normalizePlatforms func(ctx context.Context, bearer string) (collectionclient.Result, error)
	normalizeRegions   func(ctx context.Context, bearer string) (collectionclient.Result, error)

	sharedShelf        func(ctx context.Context, bearer string, id uuid.UUID) (collectionapi.SharedShelf, error)
	sharedShelfBySlug  func(ctx context.Context, bearer string, ownerID uuid.UUID, slug string) (collectionapi.SharedShelf, error)
	sharedShelfEntries func(ctx context.Context, bearer string, id uuid.UUID, limit, offset *int) (collectionclient.Result, error)
	listSharedShelves  func(ctx context.Context, bearer string, ownerIDs []uuid.UUID, limit, offset int) ([]collectionapi.SharedShelfSummary, int, error)
	sharedShelvesByIDs func(ctx context.Context, bearer string, ids []uuid.UUID) ([]collectionapi.SharedShelfSummary, error)

	mu        sync.Mutex
	gotBearer []string
	gotAuth   []string
	gotOps    []string
}

func (s *stubCollection) call(op, bearer string) (collectionclient.Result, error) {
	s.mu.Lock()
	s.gotOps = append(s.gotOps, op)
	s.gotBearer = append(s.gotBearer, bearer)
	s.gotAuth = append(s.gotAuth, "Bearer "+bearer)
	s.mu.Unlock()
	if s.answer == nil {
		panic("stubCollection: unexpected call: " + op)
	}
	return s.answer(op)
}

// opCount reports how many recorded calls carried op; hit counters below are thin wrappers over it.
func (s *stubCollection) opCount(op string) int {
	s.mu.Lock()
	defer s.mu.Unlock()
	n := 0
	for _, o := range s.gotOps {
		if o == op {
			n++
		}
	}
	return n
}

func (s *stubCollection) callCount() int            { return s.opCount("list_entries") }
func (s *stubCollection) createCallCount() int      { return s.opCount("create_entry") }
func (s *stubCollection) valueHistoryHitCount() int { return s.opCount("value_history") }

func (s *stubCollection) ListEntries(_ context.Context, bearer string, _ *collectionapi.ListEntriesParams) (collectionclient.Result, error) {
	return s.call("list_entries", bearer)
}

func (s *stubCollection) CreateEntry(_ context.Context, bearer string, _ []byte) (collectionclient.Result, error) {
	return s.call("create_entry", bearer)
}

func (s *stubCollection) GetEntry(_ context.Context, bearer string, _ uuid.UUID) (collectionclient.Result, error) {
	return s.call("get_entry", bearer)
}

func (s *stubCollection) UpdateEntry(_ context.Context, bearer string, _ uuid.UUID, _ []byte) (collectionclient.Result, error) {
	return s.call("update_entry", bearer)
}

func (s *stubCollection) DeleteEntry(_ context.Context, bearer string, _ uuid.UUID) (collectionclient.Result, error) {
	return s.call("delete_entry", bearer)
}

func (s *stubCollection) AckRegionMismatch(_ context.Context, bearer string, _ uuid.UUID) (collectionclient.Result, error) {
	return s.call("ack_region_mismatch", bearer)
}

func (s *stubCollection) ReorderEntry(_ context.Context, bearer string, _ uuid.UUID, _ []byte) (collectionclient.Result, error) {
	return s.call("reorder_entry", bearer)
}

func (s *stubCollection) BulkUpdateEntries(_ context.Context, bearer string, _ []byte) (collectionclient.Result, error) {
	return s.call("bulk_update_entries", bearer)
}

func (s *stubCollection) ListTags(_ context.Context, bearer string) (collectionclient.Result, error) {
	return s.call("list_tags", bearer)
}

func (s *stubCollection) CreateTag(_ context.Context, bearer string, _ []byte) (collectionclient.Result, error) {
	return s.call("create_tag", bearer)
}

func (s *stubCollection) RenameTag(_ context.Context, bearer string, _ uuid.UUID, _ []byte) (collectionclient.Result, error) {
	return s.call("rename_tag", bearer)
}

func (s *stubCollection) DeleteTag(_ context.Context, bearer string, _ uuid.UUID) (collectionclient.Result, error) {
	return s.call("delete_tag", bearer)
}

func (s *stubCollection) ListViews(_ context.Context, bearer string) (collectionclient.Result, error) {
	return s.call("list_views", bearer)
}

func (s *stubCollection) CreateView(_ context.Context, bearer string, _ []byte) (collectionclient.Result, error) {
	return s.call("create_view", bearer)
}

func (s *stubCollection) UpdateView(_ context.Context, bearer string, _ uuid.UUID, _ []byte) (collectionclient.Result, error) {
	return s.call("update_view", bearer)
}

func (s *stubCollection) DeleteView(_ context.Context, bearer string, _ uuid.UUID) (collectionclient.Result, error) {
	return s.call("delete_view", bearer)
}

func (s *stubCollection) GetDashboard(_ context.Context, bearer string, _ *collectionapi.GetDashboardParams) (collectionclient.Result, error) {
	return s.call("dashboard", bearer)
}

func (s *stubCollection) GetValueHistory(_ context.Context, bearer string) (collectionclient.Result, error) {
	return s.call("value_history", bearer)
}

func (s *stubCollection) LibrarySummary(ctx context.Context, bearer string) (collectionapi.LibrarySummary, error) {
	if s.library == nil {
		panic("stubCollection: unexpected LibrarySummary")
	}
	return s.library(ctx, bearer)
}

func (s *stubCollection) PurgeUserData(_ context.Context, bearer string) (collectionclient.Result, error) {
	return s.call("purge_user_data", bearer)
}

func (s *stubCollection) CountProductReferences(_ context.Context, bearer string, _ uuid.UUID) (collectionclient.Result, error) {
	return s.call("count_product_references", bearer)
}

func (s *stubCollection) CreateSubmission(ctx context.Context, bearer string, id uuid.UUID) (collectionclient.Result, error) {
	if s.createSubmission == nil {
		panic("stubCollection: unexpected CreateSubmission")
	}
	return s.createSubmission(ctx, bearer, id)
}

func (s *stubCollection) GetSubmission(ctx context.Context, bearer string, id uuid.UUID) (collectionclient.Result, error) {
	if s.getSubmission == nil {
		panic("stubCollection: unexpected GetSubmission")
	}
	return s.getSubmission(ctx, bearer, id)
}

func (s *stubCollection) CancelSubmission(ctx context.Context, bearer string, id uuid.UUID) (collectionclient.Result, error) {
	if s.cancelSubmission == nil {
		panic("stubCollection: unexpected CancelSubmission")
	}
	return s.cancelSubmission(ctx, bearer, id)
}

func (s *stubCollection) AckSubmission(ctx context.Context, bearer string, id uuid.UUID) (collectionclient.Result, error) {
	if s.ackSubmission == nil {
		panic("stubCollection: unexpected AckSubmission")
	}
	return s.ackSubmission(ctx, bearer, id)
}

func (s *stubCollection) ListSubmissions(ctx context.Context, bearer string, params *collectionapi.ListSubmissionsParams) (collectionclient.Result, error) {
	if s.listSubmissions == nil {
		panic("stubCollection: unexpected ListSubmissions")
	}
	return s.listSubmissions(ctx, bearer, params)
}

func (s *stubCollection) SubmitVerdict(ctx context.Context, bearer string, id uuid.UUID, body []byte) (collectionclient.Result, error) {
	if s.submitVerdict == nil {
		panic("stubCollection: unexpected SubmitVerdict")
	}
	return s.submitVerdict(ctx, bearer, id, body)
}

func (s *stubCollection) TriggerRematch(ctx context.Context, bearer string) (collectionclient.Result, error) {
	if s.triggerRematch == nil {
		panic("stubCollection: unexpected TriggerRematch")
	}
	return s.triggerRematch(ctx, bearer)
}

func (s *stubCollection) Resnapshot(ctx context.Context, bearer string) (collectionclient.Result, error) {
	if s.resnapshot == nil {
		panic("stubCollection: unexpected Resnapshot")
	}
	return s.resnapshot(ctx, bearer)
}

func (s *stubCollection) NormalizePlatforms(ctx context.Context, bearer string) (collectionclient.Result, error) {
	if s.normalizePlatforms == nil {
		panic("stubCollection: unexpected NormalizePlatforms")
	}
	return s.normalizePlatforms(ctx, bearer)
}

func (s *stubCollection) NormalizeRegions(ctx context.Context, bearer string) (collectionclient.Result, error) {
	if s.normalizeRegions == nil {
		panic("stubCollection: unexpected NormalizeRegions")
	}
	return s.normalizeRegions(ctx, bearer)
}

func (s *stubCollection) SharedShelf(ctx context.Context, bearer string, id uuid.UUID) (collectionapi.SharedShelf, error) {
	if s.sharedShelf == nil {
		panic("stubCollection: unexpected SharedShelf")
	}
	return s.sharedShelf(ctx, bearer, id)
}

func (s *stubCollection) SharedShelfBySlug(ctx context.Context, bearer string, ownerID uuid.UUID, slug string) (collectionapi.SharedShelf, error) {
	if s.sharedShelfBySlug == nil {
		panic("stubCollection: unexpected SharedShelfBySlug")
	}
	return s.sharedShelfBySlug(ctx, bearer, ownerID, slug)
}

func (s *stubCollection) SharedShelfEntries(ctx context.Context, bearer string, id uuid.UUID, limit, offset *int) (collectionclient.Result, error) {
	if s.sharedShelfEntries == nil {
		panic("stubCollection: unexpected SharedShelfEntries")
	}
	return s.sharedShelfEntries(ctx, bearer, id, limit, offset)
}

func (s *stubCollection) ListSharedShelves(ctx context.Context, bearer string, ownerIDs []uuid.UUID, limit, offset int) ([]collectionapi.SharedShelfSummary, int, error) {
	if s.listSharedShelves == nil {
		panic("stubCollection: unexpected ListSharedShelves")
	}
	return s.listSharedShelves(ctx, bearer, ownerIDs, limit, offset)
}

func (s *stubCollection) SharedShelvesByIDs(ctx context.Context, bearer string, ids []uuid.UUID) ([]collectionapi.SharedShelfSummary, error) {
	if s.sharedShelvesByIDs == nil {
		panic("stubCollection: unexpected SharedShelvesByIDs")
	}
	return s.sharedShelvesByIDs(ctx, bearer, ids)
}

var _ CollectionAPI = (*stubCollection)(nil)
