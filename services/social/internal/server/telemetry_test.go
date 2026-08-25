// Tests the shared internalError and capExceeded helpers directly; domain
// counters ride through handlers_test.go's response-shape assertions.
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

// syncBuffer is a mutex-guarded buffer: the handler goroutine writes log
// lines while the test goroutine reads them back.
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
// tests that inspect the log line, not just the response.
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

// capRejectionCount reads the capRejections point for kind (0 if never
// incremented), binding the metric name once instead of at each call site.
func capRejectionCount(t *testing.T, reader *sdkmetric.ManualReader, kind string) int64 {
	t.Helper()
	return metrictest.Int64Sum(t, reader, "vg.social.caps.rejections", attribute.String("kind", kind))
}

// wantCapExceeded asserts a 429 cap_exceeded problem with wantDetail
// verbatim; the message text itself is the point, not just the status/code.
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

// internalError's 500 body stays generic detail text; the log line
// carries the op and cause the body never could. Unfollow is the
// representative site (single store call, no other collaborators).
func TestUnitInternalErrorLogCarriesCause(t *testing.T) {
	boom := errors.New("pg exploded")
	st := &stubStore{unfollow: func(context.Context, uuid.UUID, uuid.UUID) error { return boom }}
	srv, a, buf := newLoggedServer(t, st, &stubCollection{}, &stubUsers{})
	resp := do(t, http.MethodDelete, srv.URL+"/follows/"+uuid.NewString(), a.token(t, uuid.NewString()), nil)
	wantProblem(t, resp, http.StatusInternalServerError, "internal")

	line := findLine(buf.lines(t), "handler error")
	if line == nil {
		t.Fatal("no handler error log line")
	}
	if line["level"] != "ERROR" || line["op"] != "unfollow" || !strings.Contains(fmt.Sprint(line["err"]), "pg exploded") {
		t.Fatalf("handler error line: %v", line)
	}
}

// The three TestUnitCapExceeded_* tests each drive a real cap-exceeded
// branch, checking both the capRejections kind label and the response detail.

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
