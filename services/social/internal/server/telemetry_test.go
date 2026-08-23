// Telemetry emission tests: the shared internalError and capExceeded
// helpers' log/metric/response shape. Everything else domain-counter
// related (follows, likes, comments, feedReads, publishEvents,
// purgeRuns) rides through handlers_test.go's ordinary response-shape
// assertions; these two helpers get direct pinning tests of their own
// (collection's TestUnitInternalErrorLogCarriesCause is the model
// both mirror).
package server_test

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/google/uuid"
	"go.opentelemetry.io/otel/attribute"
	sdkmetric "go.opentelemetry.io/otel/sdk/metric"

	"github.com/levonn-dev/vgkeep/libs/go/metrictest"
	"github.com/levonn-dev/vgkeep/services/social/internal/collectionclient"
	"github.com/levonn-dev/vgkeep/services/social/internal/server"
	"github.com/levonn-dev/vgkeep/services/social/internal/store"
	"github.com/levonn-dev/vgkeep/services/social/internal/userclient"
)

// syncBuffer is a mutex-guarded buffer: the httptest server's handler
// goroutine writes log lines while the test goroutine reads them back.
// Mirrors collection's syncBuffer (services/collection/internal/server/telemetry_test.go).
type syncBuffer struct {
	mu  sync.Mutex
	buf strings.Builder
}

func (b *syncBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.Write(p)
}

func (b *syncBuffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.String()
}

// lines decodes every JSON log line written so far.
func (b *syncBuffer) lines(t *testing.T) []map[string]any {
	t.Helper()
	text := strings.TrimSpace(b.String())
	if text == "" {
		return nil
	}
	var out []map[string]any
	for _, line := range strings.Split(text, "\n") {
		if line == "" {
			continue
		}
		var m map[string]any
		if err := json.Unmarshal([]byte(line), &m); err != nil {
			t.Fatalf("bad log line %q: %v", line, err)
		}
		out = append(out, m)
	}
	return out
}

func findLine(lines []map[string]any, msg string) map[string]any {
	for _, l := range lines {
		if l["msg"] == msg {
			return l
		}
	}
	return nil
}

// newLoggedServer is newUnitServer with a capturing JSON logger, for
// tests that must inspect the emitted log line rather than just the
// response.
func newLoggedServer(t *testing.T, st server.Store, col server.Collection, users server.Users) (*httptest.Server, authEnv, *syncBuffer) {
	t.Helper()
	buf := &syncBuffer{}
	logger := slog.New(slog.NewJSONHandler(buf, nil))
	a := newAuthEnv(t)
	h := server.New(st, col, users, server.Options{
		Logger: logger, CapComments: 50, CapFollows: 100, CapLikes: 200,
	})
	router, err := server.NewRouter(h, a.v, logger, func(context.Context) error { return nil })
	if err != nil {
		t.Fatal(err)
	}
	srv := httptest.NewServer(router)
	t.Cleanup(srv.Close)
	return srv, a, buf
}

// capRejectionCount reads the capRejections counter's point for kind
// (0 when that kind never incremented). Stays local rather than a bare
// metrictest.Int64Sum call at each of the 3 sites below: binding the
// fixed metric name once here, behind a domain-readable name, is the
// same "genuinely adapts" call collection's collectDomainMetrics
// makes for its own scope constant.
func capRejectionCount(t *testing.T, reader *sdkmetric.ManualReader, kind string) int64 {
	t.Helper()
	return metrictest.Int64Sum(t, reader, "vg.social.caps.rejections", attribute.String("kind", kind))
}

// wantCapExceeded asserts a 429 cap_exceeded problem carrying
// wantDetail verbatim - capExceeded's job is to preserve each of its
// three call sites' exact pre-helper message, so the message itself
// (not just the status/code the existing per-handler tests already
// check) is the point of this assertion.
func wantCapExceeded(t *testing.T, resp *http.Response, wantDetail string) {
	t.Helper()
	if resp.StatusCode != http.StatusTooManyRequests {
		t.Fatalf("status: got %d, want %d", resp.StatusCode, http.StatusTooManyRequests)
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
	if p.Detail != wantDetail {
		t.Fatalf("detail: got %q, want %q", p.Detail, wantDetail)
	}
}

// TestUnitInternalErrorLogCarriesCause pins the shared 500 helper:
// the problem body stays the generic detail text a caller already
// saw, while the log line carries the op and cause that text never
// could. Unfollow is the representative site (a single store call, no
// collection/users collaborators to wire up). Mirrors collection's
// TestUnitInternalErrorLogCarriesCause.
func TestUnitInternalErrorLogCarriesCause(t *testing.T) {
	boom := errors.New("pg exploded")
	st := &stubStore{unfollow: func(context.Context, uuid.UUID, uuid.UUID) error { return boom }}
	srv, a, buf := newLoggedServer(t, st, &stubCollection{}, &stubUsers{})
	resp := do(t, http.MethodDelete, srv.URL+"/follows/"+uuid.NewString(), a.token(t, uuid.NewString()), nil)
	wantProblem(t, resp, http.StatusInternalServerError, "internal")

	line := findLine(buf.lines(t), "store error")
	if line == nil {
		t.Fatal("no store error log line")
	}
	if line["level"] != "ERROR" || line["op"] != "unfollow" || !strings.Contains(fmt.Sprint(line["err"]), "pg exploded") {
		t.Fatalf("store error line: %v", line)
	}
}

// TestUnitCapExceeded_Follow, TestUnitCapExceeded_LikeShelf, and
// TestUnitCapExceeded_CreateShelfComment each drive their real site's
// cap-exceeded branch (fixtures mirror the "cap maps to 429" /
// "cap 429s..." cases already in handlers_test.go) and check both
// halves capExceeded(w, r, kind) must preserve: the capRejections
// counter's kind label and the exact response detail text.

func TestUnitCapExceeded_Follow(t *testing.T) {
	reader := metrictest.Install(t)
	me, other := uuid.New(), uuid.New()
	st := &stubStore{follow: func(context.Context, uuid.UUID, uuid.UUID, int) (bool, error) {
		return false, store.ErrCapExceeded
	}}
	users := &stubUsers{cardsByIDs: func(context.Context, string, []uuid.UUID) ([]userclient.Card, error) {
		return []userclient.Card{{UserID: other, Visibility: "listed"}}, nil
	}}
	srv, a := newUnitServer(t, st, &stubCollection{}, users)

	resp := do(t, http.MethodPut, srv.URL+"/follows/"+other.String(), a.token(t, me.String()), nil)
	wantCapExceeded(t, resp, "follow limit reached; try again later")
	if got := capRejectionCount(t, reader, "follows"); got != 1 {
		t.Fatalf("caps.rejections{kind=follows} = %d, want 1", got)
	}
}

func TestUnitCapExceeded_LikeShelf(t *testing.T) {
	reader := metrictest.Install(t)
	me, owner, shelf := uuid.New(), uuid.New(), uuid.New()
	col := &stubCollection{sharedShelf: func(context.Context, string, uuid.UUID) (collectionclient.Shelf, error) {
		return collectionclient.Shelf{ID: shelf, OwnerID: owner, Visibility: "listed"}, nil
	}}
	users := &stubUsers{cardsByIDs: func(context.Context, string, []uuid.UUID) ([]userclient.Card, error) {
		return []userclient.Card{{UserID: owner, Visibility: "listed"}}, nil
	}}
	st := &stubStore{like: func(context.Context, uuid.UUID, uuid.UUID, uuid.UUID, int) (bool, error) {
		return false, store.ErrCapExceeded
	}}
	srv, a := newUnitServer(t, st, col, users)

	resp := do(t, http.MethodPut, srv.URL+"/likes/"+shelf.String(), a.token(t, me.String()), nil)
	wantCapExceeded(t, resp, "like limit reached; try again later")
	if got := capRejectionCount(t, reader, "likes"); got != 1 {
		t.Fatalf("caps.rejections{kind=likes} = %d, want 1", got)
	}
}

func TestUnitCapExceeded_CreateShelfComment(t *testing.T) {
	reader := metrictest.Install(t)
	me, owner, shelf := uuid.New(), uuid.New(), uuid.New()
	col := &stubCollection{sharedShelf: func(context.Context, string, uuid.UUID) (collectionclient.Shelf, error) {
		return collectionclient.Shelf{ID: shelf, OwnerID: owner, Visibility: "listed"}, nil
	}}
	st := &stubStore{createComment: func(context.Context, uuid.UUID, uuid.UUID, uuid.UUID, string, int) (store.Comment, error) {
		return store.Comment{}, store.ErrCapExceeded
	}}
	srv, a := newUnitServer(t, st, col, &stubUsers{})

	resp := do(t, http.MethodPost, srv.URL+"/shelves/"+shelf.String()+"/comments",
		a.token(t, me.String()), map[string]string{"body": "x"})
	wantCapExceeded(t, resp, "comment limit reached; try again later")
	if got := capRejectionCount(t, reader, "comments"); got != 1 {
		t.Fatalf("caps.rejections{kind=comments} = %d, want 1", got)
	}
}
