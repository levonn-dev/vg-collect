// Tests for catalog submission review.

package server_test

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/levonn-dev/vgkeep/libs/go/contract/common"
	"github.com/levonn-dev/vgkeep/libs/go/contract/enrichapi"
	"github.com/levonn-dev/vgkeep/services/collection/internal/enrichmentclient"
	"github.com/levonn-dev/vgkeep/services/collection/internal/gen/api"
	"github.com/levonn-dev/vgkeep/services/collection/internal/store"
)

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
		Community: &common.CommunityMeta{PlatformName: &platName,
			Developers: &[]string{"Garage Team"}, Publishers: &[]string{"Repro House"}}}

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
			if len(snap.Developers) != 1 || snap.Developers[0] != "Garage Team" ||
				len(snap.Publishers) != 1 || snap.Publishers[0] != "Repro House" {
				t.Fatalf("adopt snapshot must carry the community credits: %v/%v", snap.Developers, snap.Publishers)
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
		"developers": []string{"Garage Team"}, "publishers": []string{"Repro House"},
	}}
	resp := do(t, http.MethodPost, srv.URL+"/admin/submissions/"+subID.String()+"/verdict", admin, jsonBody(body))
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("approve_new: %d", resp.StatusCode)
	}
	if mintedReq == nil || mintedReq.Name != "Repro Alpha" || mintedReq.Edition == nil || *mintedReq.Edition != "glow cart" {
		t.Fatalf("mint request: %+v", mintedReq)
	}
	if mintedReq.Developers == nil || len(*mintedReq.Developers) != 1 || (*mintedReq.Developers)[0] != "Garage Team" ||
		mintedReq.Publishers == nil || len(*mintedReq.Publishers) != 1 || (*mintedReq.Publishers)[0] != "Repro House" {
		t.Fatalf("mint request must carry the curated credits: %+v", mintedReq)
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
			return enrichapi.Product{Id: minted, Type: "game", Name: "Repro", Community: &common.CommunityMeta{CoverUrl: &cu}}, nil
		},
		getProduct: func(_ context.Context, _ string, id uuid.UUID) (enrichapi.Product, error) {
			cu := "https://img.example/sub.jpg"
			return enrichapi.Product{Id: id, Type: "game", Name: "Repro", Community: &common.CommunityMeta{CoverUrl: &cu}}, nil
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
