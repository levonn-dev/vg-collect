package server

import (
	"bytes"
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
	"time"

	"github.com/google/uuid"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/metric"
	"go.opentelemetry.io/otel/metric/embedded"

	"github.com/levonn-dev/vgkeep/services/bff/internal/authclient"
	"github.com/levonn-dev/vgkeep/services/bff/internal/enrichmentclient"
	"github.com/levonn-dev/vgkeep/services/bff/internal/gen/collectionapi"
	"github.com/levonn-dev/vgkeep/services/bff/internal/gen/enrichapi"
	"github.com/levonn-dev/vgkeep/services/bff/internal/gen/userapi"
	"github.com/levonn-dev/vgkeep/services/bff/internal/session"
	"github.com/levonn-dev/vgkeep/services/bff/internal/userclient"
)

// stubCounter records Add calls so tests can assert the exact bounded
// attributes each decision path emits (under test no SDK meter is
// installed; probeMetrics swaps these in for the noop instruments New
// hands out).
type stubCounter struct {
	embedded.Int64Counter
	mu   sync.Mutex
	adds []counterAdd
}

type counterAdd struct {
	n     int64
	attrs attribute.Set
}

func (c *stubCounter) Add(_ context.Context, n int64, opts ...metric.AddOption) {
	set := metric.NewAddConfig(opts).Attributes()
	c.mu.Lock()
	defer c.mu.Unlock()
	c.adds = append(c.adds, counterAdd{n: n, attrs: set})
}

func (c *stubCounter) Enabled(context.Context) bool { return true }

// total sums recorded adds whose attribute set equals attrs exactly.
func (c *stubCounter) total(attrs ...attribute.KeyValue) int64 {
	want := attribute.NewSet(attrs...)
	c.mu.Lock()
	defer c.mu.Unlock()
	var sum int64
	for _, a := range c.adds {
		if a.attrs.Equals(&want) {
			sum += a.n
		}
	}
	return sum
}

func (c *stubCounter) count() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return len(c.adds)
}

func (c *stubCounter) snapshot() []counterAdd {
	c.mu.Lock()
	defer c.mu.Unlock()
	return append([]counterAdd(nil), c.adds...)
}

// stubCacheResultErr fails only the published-result read, so a test
// can hold the waiter path on a healthy lock while the poll errors.
type stubCacheResultErr struct {
	*stubCache
	resultErr error
}

func (s *stubCacheResultErr) GetRefreshResult(context.Context, string) (string, error) {
	return "", s.resultErr
}

// metricsProbe carries the recording counters swapped onto a Handlers.
type metricsProbe struct {
	logins, refreshes, lookups *stubCounter
}

func probeMetrics(h *Handlers) *metricsProbe {
	p := &metricsProbe{logins: &stubCounter{}, refreshes: &stubCounter{}, lookups: &stubCounter{}}
	h.logins, h.refreshes, h.cacheLookups = p.logins, p.refreshes, p.lookups
	return p
}

// assertOnly fails unless c recorded exactly one add, carrying attrs.
func assertOnly(t *testing.T, c *stubCounter, attrs ...attribute.KeyValue) {
	t.Helper()
	if got := c.count(); got != 1 {
		t.Fatalf("adds = %v, want exactly one", c.snapshot())
	}
	if got := c.total(attrs...); got != 1 {
		t.Fatalf("adds with %v = %d (recorded %v)", attrs, got, c.snapshot())
	}
}

func flowOutcome(flow, outcome string) []attribute.KeyValue {
	return []attribute.KeyValue{attribute.String("flow", flow), attribute.String("outcome", outcome)}
}

func TestUnitLoginOutcomeMapping(t *testing.T) {
	loginCases := []struct {
		err  error
		want string
	}{
		{authclient.ErrEmailUnverified, "email_unverified"},
		{fmt.Errorf("wrapped: %w", authclient.ErrProviderError), "provider_error"},
		{authclient.ErrLoginFailed, "failed"},
		{errors.New("boom"), "failed"},
	}
	for _, tc := range loginCases {
		if got := loginOutcome(tc.err); got != tc.want {
			t.Errorf("loginOutcome(%v) = %q, want %q", tc.err, got, tc.want)
		}
	}
	linkCases := []struct {
		err           error
		outcome, code string
	}{
		{authclient.ErrLinkConflict, "conflict", "conflict"},
		{fmt.Errorf("wrapped: %w", authclient.ErrLinkEmailUnverified), "email_unverified", "email_unverified"},
		{errors.New("boom"), "failed", "link_failed"},
	}
	for _, tc := range linkCases {
		if got := linkOutcome(tc.err); got != tc.outcome {
			t.Errorf("linkOutcome(%v) = %q, want %q", tc.err, got, tc.outcome)
		}
		if got := linkErrorCode(tc.err); got != tc.code {
			t.Errorf("linkErrorCode(%v) = %q, want %q", tc.err, got, tc.code)
		}
	}
}

// TestUnitRelayEnrichment_UpstreamErrorIs502 and
// TestUnitRelayUser_UpstreamErrorIs502 pin relayEnrichment and
// relayUser's error classification directly, the same way
// TestUnitLoginOutcomeMapping pins loginOutcome above: a dead client
// is an infrastructure fault, answered with the fixed upstream_error
// problem, never the client's own error text. relayCollection and
// relaySocial are the pattern (see server.go and handlers_social.go);
// these two round the set out to all four backend services and are
// exercised directly since a handler-level test would only prove one
// caller's wiring, not the helper's own contract.
func TestUnitRelayEnrichment_UpstreamErrorIs502(t *testing.T) {
	h := &Handlers{}
	rec := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodGet, "/x", nil)
	h.relayEnrichment(rec, r, enrichmentclient.Result{}, enrichmentclient.ErrUpstream)
	if rec.Code != http.StatusBadGateway {
		t.Fatalf("code = %d, want 502", rec.Code)
	}
	var p struct {
		Code   string `json:"code"`
		Detail string `json:"detail"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &p); err != nil {
		t.Fatalf("problem body: %v (%s)", err, rec.Body.String())
	}
	if p.Code != "upstream_error" || p.Detail != "enrichment service unavailable" {
		t.Fatalf("problem = %+v", p)
	}
}

// TestUnitRelayEnrichment_PassThroughRelaysVerbatim pins the happy
// path: no client error relays the upstream status, content type, and
// body exactly, untouched.
func TestUnitRelayEnrichment_PassThroughRelaysVerbatim(t *testing.T) {
	h := &Handlers{}
	rec := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodGet, "/x", nil)
	res := enrichmentclient.Result{Status: 201, ContentType: "application/json", Body: []byte(`{"ok":true}`)}
	h.relayEnrichment(rec, r, res, nil)
	if rec.Code != 201 || rec.Body.String() != `{"ok":true}` || rec.Header().Get("Content-Type") != "application/json" {
		t.Fatalf("relay: %d %s %q", rec.Code, rec.Body.String(), rec.Header().Get("Content-Type"))
	}
}

func TestUnitRelayUser_UpstreamErrorIs502(t *testing.T) {
	h := &Handlers{}
	rec := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodGet, "/x", nil)
	h.relayUser(rec, r, userclient.Result{}, userclient.ErrUpstream)
	if rec.Code != http.StatusBadGateway {
		t.Fatalf("code = %d, want 502", rec.Code)
	}
	var p struct {
		Code   string `json:"code"`
		Detail string `json:"detail"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &p); err != nil {
		t.Fatalf("problem body: %v (%s)", err, rec.Body.String())
	}
	if p.Code != "upstream_error" || p.Detail != "user service unavailable" {
		t.Fatalf("problem = %+v", p)
	}
}

// TestUnitRelayUser_PassThroughRelaysVerbatim mirrors
// TestUnitRelayEnrichment_PassThroughRelaysVerbatim for the user
// service.
func TestUnitRelayUser_PassThroughRelaysVerbatim(t *testing.T) {
	h := &Handlers{}
	rec := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodGet, "/x", nil)
	res := userclient.Result{Status: 200, ContentType: "application/json", Body: []byte(`{"users":[]}`)}
	h.relayUser(rec, r, res, nil)
	if rec.Code != 200 || rec.Body.String() != `{"users":[]}` || rec.Header().Get("Content-Type") != "application/json" {
		t.Fatalf("relay: %d %s %q", rec.Code, rec.Body.String(), rec.Header().Get("Content-Type"))
	}
}

func TestUnitLoginMetric_LoginFlow(t *testing.T) {
	access := mintAccess(t, "u1", "j1", time.Now().Add(5*time.Minute))
	pair := authclient.TokenPair{AccessToken: access, RefreshToken: "r1",
		ExpiresIn: 300, RefreshExpiresIn: 2000}
	google := "google"

	cases := []struct {
		name          string
		auth          *stubAuthFull
		path          string
		flow, outcome string
	}{
		{
			name: "dev_success",
			auth: &stubAuthFull{dev: func(context.Context, string) (authclient.TokenPair, error) {
				return pair, nil
			}},
			path: "/api/auth/login?provider=dev&user=alice", flow: "login", outcome: "success",
		},
		{
			name: "dev_email_unverified",
			auth: &stubAuthFull{dev: func(context.Context, string) (authclient.TokenPair, error) {
				return authclient.TokenPair{}, authclient.ErrEmailUnverified
			}},
			path: "/api/auth/login?provider=dev", flow: "login", outcome: "email_unverified",
		},
		{
			name: "start_provider_error",
			auth: &stubAuthFull{start: func(context.Context, string) (string, error) {
				return "", authclient.ErrProviderError
			}},
			path: "/api/auth/login?provider=google", flow: "login", outcome: "provider_error",
		},
		{
			name: "start_failure",
			auth: &stubAuthFull{start: func(context.Context, string) (string, error) {
				return "", errors.New("boom")
			}},
			path: "/api/auth/login?provider=google", flow: "login", outcome: "failed",
		},
		{
			name: "callback_success",
			auth: &stubAuthFull{callback: func(context.Context, string, string) (authclient.TokenPair, error) {
				return pair, nil
			}},
			path: "/api/auth/callback?code=c&state=s", flow: "login", outcome: "success",
		},
		{
			name: "callback_failure",
			auth: &stubAuthFull{callback: func(context.Context, string, string) (authclient.TokenPair, error) {
				return authclient.TokenPair{}, authclient.ErrLoginFailed
			}},
			path: "/api/auth/callback?code=c&state=s", flow: "login", outcome: "failed",
		},
		{
			name: "callback_missing_params",
			auth: &stubAuthFull{},
			path: "/api/auth/callback", flow: "login", outcome: "failed",
		},
		{
			name: "callback_link_success",
			auth: &stubAuthFull{callback: func(context.Context, string, string) (authclient.TokenPair, error) {
				p := pair
				p.LinkedProvider = &google
				return p, nil
			}},
			path: "/api/auth/callback?code=c&state=s", flow: "link", outcome: "success",
		},
		{
			name: "callback_link_conflict",
			auth: &stubAuthFull{callback: func(context.Context, string, string) (authclient.TokenPair, error) {
				return authclient.TokenPair{}, authclient.ErrLinkConflict
			}},
			path: "/api/auth/callback?code=c&state=s", flow: "link", outcome: "conflict",
		},
		{
			name: "callback_link_email_unverified",
			auth: &stubAuthFull{callback: func(context.Context, string, string) (authclient.TokenPair, error) {
				return authclient.TokenPair{}, authclient.ErrLinkEmailUnverified
			}},
			path: "/api/auth/callback?code=c&state=s", flow: "link", outcome: "email_unverified",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			h := newTestHandlers(t, newStubCache(), tc.auth)
			p := probeMetrics(h)
			rec := httptest.NewRecorder()
			newRouterFor(t, h).ServeHTTP(rec, httptest.NewRequest(http.MethodGet, tc.path, nil))
			assertOnly(t, p.logins, flowOutcome(tc.flow, tc.outcome)...)
		})
	}

	t.Run("idp_redirect_counts_nothing", func(t *testing.T) {
		h := newTestHandlers(t, newStubCache(), &stubAuthFull{
			start: func(context.Context, string) (string, error) {
				return "https://idp.example/authorize?state=s", nil
			},
		})
		p := probeMetrics(h)
		rec := httptest.NewRecorder()
		newRouterFor(t, h).ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/auth/login?provider=google", nil))
		if p.logins.count() != 0 {
			t.Fatalf("the attempt completes at the callback; adds = %v", p.logins.snapshot())
		}
	})
}

func TestUnitLoginMetric_LinkFlow(t *testing.T) {
	linked := "dev"
	linkedAccess := mintAccess(t, "u1", "jlinked", time.Now().Add(5*time.Minute))

	drive := func(t *testing.T, auth *stubAuthFull, path string) *metricsProbe {
		t.Helper()
		h := newTestHandlers(t, newStubCache(), auth)
		p := probeMetrics(h)
		sessAccess := mintAccess(t, "u1", "jsess", time.Now().Add(5*time.Minute))
		r := httptest.NewRequest(http.MethodGet, path, nil)
		r.AddCookie(sealedCookie(t, h, sessAccess, "rsess"))
		rec := httptest.NewRecorder()
		newRouterFor(t, h).ServeHTTP(rec, r)
		if rec.Code != http.StatusFound {
			t.Fatalf("code = %d", rec.Code)
		}
		return p
	}

	t.Run("dev_link_success", func(t *testing.T) {
		p := drive(t, &stubAuthFull{devLink: func(context.Context, string, string) (authclient.TokenPair, error) {
			return authclient.TokenPair{AccessToken: linkedAccess, RefreshToken: "r2",
				ExpiresIn: 300, RefreshExpiresIn: 2000, LinkedProvider: &linked}, nil
		}}, "/api/auth/link?provider=dev&user=bob")
		assertOnly(t, p.logins, flowOutcome("link", "success")...)
	})
	t.Run("dev_link_conflict", func(t *testing.T) {
		p := drive(t, &stubAuthFull{devLink: func(context.Context, string, string) (authclient.TokenPair, error) {
			return authclient.TokenPair{}, authclient.ErrLinkConflict
		}}, "/api/auth/link?provider=dev&user=bob")
		assertOnly(t, p.logins, flowOutcome("link", "conflict")...)
	})
	t.Run("dev_link_email_unverified", func(t *testing.T) {
		p := drive(t, &stubAuthFull{devLink: func(context.Context, string, string) (authclient.TokenPair, error) {
			return authclient.TokenPair{}, authclient.ErrLinkEmailUnverified
		}}, "/api/auth/link?provider=dev&user=bob")
		assertOnly(t, p.logins, flowOutcome("link", "email_unverified")...)
	})
	t.Run("link_start_failure", func(t *testing.T) {
		p := drive(t, &stubAuthFull{linkStart: func(context.Context, string, string) (string, error) {
			return "", errors.New("boom")
		}}, "/api/auth/link?provider=google")
		assertOnly(t, p.logins, flowOutcome("link", "failed")...)
	})
	t.Run("link_start_redirect_counts_nothing", func(t *testing.T) {
		p := drive(t, &stubAuthFull{linkStart: func(context.Context, string, string) (string, error) {
			return "https://idp.example/authorize?state=link1", nil
		}}, "/api/auth/link?provider=google")
		if p.logins.count() != 0 {
			t.Fatalf("the attempt completes at the callback; adds = %v", p.logins.snapshot())
		}
	})
}

func TestUnitRefreshMetric_Outcomes(t *testing.T) {
	outcome := func(o string) attribute.KeyValue { return attribute.String("outcome", o) }
	newAccess := mintAccess(t, "u1", "j2", time.Now().Add(5*time.Minute))
	okPair := authclient.TokenPair{AccessToken: newAccess, RefreshToken: "refresh-2",
		ExpiresIn: 300, RefreshExpiresIn: 1000}

	// drive sends one /api/me request whose access token has ttl left,
	// against a prepared cache and auth stub.
	drive := func(t *testing.T, fc *stubCache, a AuthAPI, ttl time.Duration) (*Handlers, *metricsProbe, *httptest.ResponseRecorder) {
		t.Helper()
		h := newTestHandlers(t, fc, a)
		p := probeMetrics(h)
		access := mintAccess(t, "u1", "j1", time.Now().Add(ttl))
		r := httptest.NewRequest(http.MethodGet, "/api/me", nil)
		r.AddCookie(sealedCookie(t, h, access, "refresh-1"))
		return h, p, doAuth(h, r, echoNext(t, new(string)))
	}

	t.Run("rotated", func(t *testing.T) {
		_, p, rec := drive(t, newStubCache(), &stubAuth{refresh: func(context.Context, string) (authclient.TokenPair, error) {
			return okPair, nil
		}}, 10*time.Second)
		if rec.Code != http.StatusOK {
			t.Fatalf("code = %d", rec.Code)
		}
		assertOnly(t, p.refreshes, outcome("rotated"))
	})

	t.Run("rejected", func(t *testing.T) {
		_, p, _ := drive(t, newStubCache(), &stubAuth{refresh: func(context.Context, string) (authclient.TokenPair, error) {
			return authclient.TokenPair{}, authclient.ErrRefreshRejected
		}}, 10*time.Second)
		assertOnly(t, p.refreshes, outcome("rejected"))
	})

	t.Run("reuse_revoked_logs_warn", func(t *testing.T) {
		fc := newStubCache()
		h := newTestHandlers(t, fc, &stubAuth{refresh: func(context.Context, string) (authclient.TokenPair, error) {
			return authclient.TokenPair{}, &authclient.ReusedError{RevokeJTIs: []string{"jA", "jB"}}
		}})
		var logBuf bytes.Buffer
		h.logger = slog.New(slog.NewTextHandler(&logBuf, nil))
		p := probeMetrics(h)
		access := mintAccess(t, "u1", "j1", time.Now().Add(10*time.Second))
		r := httptest.NewRequest(http.MethodGet, "/api/me", nil)
		r.AddCookie(sealedCookie(t, h, access, "refresh-1"))
		doAuth(h, r, echoNext(t, new(string)))
		assertOnly(t, p.refreshes, outcome("reuse_revoked"))
		line := logBuf.String()
		if !strings.Contains(line, "refresh token reuse detected; session family revoked") ||
			!strings.Contains(line, "sub=u1") || !strings.Contains(line, "revoked_jtis=2") {
			t.Fatalf("reuse warn line missing fields: %q", line)
		}
	})

	t.Run("deferred_auth_outage_valid_token", func(t *testing.T) {
		_, p, rec := drive(t, newStubCache(), &stubAuth{refresh: func(context.Context, string) (authclient.TokenPair, error) {
			return authclient.TokenPair{}, errors.New("connection refused")
		}}, 10*time.Second)
		if rec.Code != http.StatusOK {
			t.Fatalf("code = %d", rec.Code)
		}
		assertOnly(t, p.refreshes, outcome("deferred"))
	})

	t.Run("failed_auth_outage_expired_token", func(t *testing.T) {
		_, p, rec := drive(t, newStubCache(), &stubAuth{refresh: func(context.Context, string) (authclient.TokenPair, error) {
			return authclient.TokenPair{}, errors.New("connection refused")
		}}, -time.Second)
		if rec.Code != http.StatusBadGateway {
			t.Fatalf("code = %d", rec.Code)
		}
		assertOnly(t, p.refreshes, outcome("failed"))
	})

	t.Run("deferred_user_unavailable_valid_token", func(t *testing.T) {
		_, p, _ := drive(t, newStubCache(), &stubAuth{refresh: func(context.Context, string) (authclient.TokenPair, error) {
			return authclient.TokenPair{}, authclient.ErrUserUnavailable
		}}, 10*time.Second)
		assertOnly(t, p.refreshes, outcome("deferred"))
	})

	t.Run("failed_user_unavailable_expired_token", func(t *testing.T) {
		_, p, rec := drive(t, newStubCache(), &stubAuth{refresh: func(context.Context, string) (authclient.TokenPair, error) {
			return authclient.TokenPair{}, authclient.ErrUserUnavailable
		}}, -time.Second)
		if rec.Code != http.StatusServiceUnavailable {
			t.Fatalf("code = %d", rec.Code)
		}
		assertOnly(t, p.refreshes, outcome("failed"))
	})

	t.Run("adopted_from_post_lock_read", func(t *testing.T) {
		fc := newStubCache()
		h := newTestHandlers(t, fc, &stubAuth{}) // Refresh would panic: must adopt
		p := probeMetrics(h)
		key := session.RefreshKey("refresh-1")
		sealed, err := h.codec.Seal(session.Session{AccessToken: newAccess, RefreshToken: "refresh-2"})
		if err != nil {
			t.Fatal(err)
		}
		fc.results[key] = mustResultJSON(t, sealed, 1000)
		access := mintAccess(t, "u1", "j1", time.Now().Add(10*time.Second))
		r := httptest.NewRequest(http.MethodGet, "/api/me", nil)
		r.AddCookie(sealedCookie(t, h, access, "refresh-1"))
		if rec := doAuth(h, r, echoNext(t, new(string))); rec.Code != http.StatusOK {
			t.Fatalf("code = %d", rec.Code)
		}
		assertOnly(t, p.refreshes, outcome("adopted"))
	})

	t.Run("adopted_from_poll", func(t *testing.T) {
		fc := newStubCache()
		h := newTestHandlers(t, fc, &stubAuth{})
		p := probeMetrics(h)
		key := session.RefreshKey("refresh-1")
		fc.locks[key] = "someone-else"
		sealed, err := h.codec.Seal(session.Session{AccessToken: newAccess, RefreshToken: "refresh-2"})
		if err != nil {
			t.Fatal(err)
		}
		fc.results[key] = mustResultJSON(t, sealed, 1000)
		expired := mintAccess(t, "u1", "j1", time.Now().Add(-time.Second))
		r := httptest.NewRequest(http.MethodGet, "/api/me", nil)
		r.AddCookie(sealedCookie(t, h, expired, "refresh-1"))
		if rec := doAuth(h, r, echoNext(t, new(string))); rec.Code != http.StatusOK {
			t.Fatalf("code = %d", rec.Code)
		}
		assertOnly(t, p.refreshes, outcome("adopted"))
	})

	t.Run("adopt_timeout_logs_warn", func(t *testing.T) {
		fc := newStubCache()
		h := newTestHandlers(t, fc, &stubAuth{})
		var logBuf bytes.Buffer
		h.logger = slog.New(slog.NewTextHandler(&logBuf, nil))
		p := probeMetrics(h)
		fc.locks[session.RefreshKey("refresh-1")] = "someone-else" // holder never publishes
		expired := mintAccess(t, "u1", "j1", time.Now().Add(-time.Second))
		r := httptest.NewRequest(http.MethodGet, "/api/me", nil)
		r.AddCookie(sealedCookie(t, h, expired, "refresh-1"))
		if rec := doAuth(h, r, echoNext(t, new(string))); rec.Code != http.StatusUnauthorized {
			t.Fatalf("code = %d", rec.Code)
		}
		assertOnly(t, p.refreshes, outcome("adopt_timeout"))
		line := logBuf.String()
		if !strings.Contains(line, "refresh result adoption timed out") || !strings.Contains(line, "sub=u1") {
			t.Fatalf("adoption warn line missing fields: %q", line)
		}
	})

	t.Run("failed_unparseable_rotation", func(t *testing.T) {
		_, p, rec := drive(t, newStubCache(), &stubAuth{refresh: func(context.Context, string) (authclient.TokenPair, error) {
			return authclient.TokenPair{AccessToken: "not-a-jwt", RefreshToken: "refresh-2",
				ExpiresIn: 300, RefreshExpiresIn: 1000}, nil
		}}, 10*time.Second)
		if rec.Code != http.StatusUnauthorized {
			t.Fatalf("code = %d", rec.Code)
		}
		assertOnly(t, p.refreshes, outcome("failed"))
	})

	t.Run("rotated_uncoordinated_when_valkey_down", func(t *testing.T) {
		fc := newStubCache()
		fc.err = errors.New("valkey down")
		_, p, rec := drive(t, fc, &stubAuth{refresh: func(context.Context, string) (authclient.TokenPair, error) {
			return okPair, nil
		}}, 10*time.Second)
		if rec.Code != http.StatusOK {
			t.Fatalf("code = %d", rec.Code)
		}
		assertOnly(t, p.refreshes, outcome("rotated"))
	})

	t.Run("adopt_timeout_on_result_read_error", func(t *testing.T) {
		fc := newStubCache()
		fc.locks[session.RefreshKey("refresh-1")] = "someone-else"
		cache := &stubCacheResultErr{stubCache: fc, resultErr: errors.New("valkey read failed")}
		h := newTestHandlers(t, cache, &stubAuth{})
		p := probeMetrics(h)
		expired := mintAccess(t, "u1", "j1", time.Now().Add(-time.Second))
		r := httptest.NewRequest(http.MethodGet, "/api/me", nil)
		r.AddCookie(sealedCookie(t, h, expired, "refresh-1"))
		if rec := doAuth(h, r, echoNext(t, new(string))); rec.Code != http.StatusUnauthorized {
			t.Fatalf("code = %d", rec.Code)
		}
		assertOnly(t, p.refreshes, outcome("adopt_timeout"))
	})

	t.Run("adopt_timeout_on_unreadable_result", func(t *testing.T) {
		// Three flavors of an unusable published result: not JSON, a
		// cookie that does not unseal, and a sealed session whose
		// access token does not parse.
		key := session.RefreshKey("refresh-1")
		for name, seed := range map[string]func(t *testing.T, h *Handlers) string{
			"not_json":   func(*testing.T, *Handlers) string { return "not json" },
			"unsealable": func(t *testing.T, h *Handlers) string { return mustResultJSON(t, "garbage-cookie", 1000) },
			"unparseable_jwt": func(t *testing.T, h *Handlers) string {
				sealed, err := h.codec.Seal(session.Session{AccessToken: "junk", RefreshToken: "refresh-2"})
				if err != nil {
					t.Fatal(err)
				}
				return mustResultJSON(t, sealed, 1000)
			},
		} {
			t.Run(name, func(t *testing.T) {
				fc := newStubCache()
				h := newTestHandlers(t, fc, &stubAuth{})
				p := probeMetrics(h)
				fc.locks[key] = "someone-else"
				fc.results[key] = seed(t, h)
				expired := mintAccess(t, "u1", "j1", time.Now().Add(-time.Second))
				r := httptest.NewRequest(http.MethodGet, "/api/me", nil)
				r.AddCookie(sealedCookie(t, h, expired, "refresh-1"))
				if rec := doAuth(h, r, echoNext(t, new(string))); rec.Code != http.StatusUnauthorized {
					t.Fatalf("code = %d", rec.Code)
				}
				assertOnly(t, p.refreshes, outcome("adopt_timeout"))
			})
		}
	})

	t.Run("adopt_timeout_on_canceled_request", func(t *testing.T) {
		fc := newStubCache()
		fc.locks[session.RefreshKey("refresh-1")] = "someone-else" // holder never publishes
		h := newTestHandlers(t, fc, &stubAuth{})
		p := probeMetrics(h)
		expired := mintAccess(t, "u1", "j1", time.Now().Add(-time.Second))
		r := httptest.NewRequest(http.MethodGet, "/api/me", nil)
		r.AddCookie(sealedCookie(t, h, expired, "refresh-1"))
		ctx, cancel := context.WithCancel(r.Context())
		cancel() // the browser is already gone when the poll starts
		if rec := doAuth(h, r.WithContext(ctx), echoNext(t, new(string))); rec.Code != http.StatusUnauthorized {
			t.Fatalf("code = %d", rec.Code)
		}
		assertOnly(t, p.refreshes, outcome("adopt_timeout"))
	})

	t.Run("fresh_token_counts_nothing", func(t *testing.T) {
		_, p, rec := drive(t, newStubCache(), &stubAuth{}, 5*time.Minute)
		if rec.Code != http.StatusOK || p.refreshes.count() != 0 {
			t.Fatalf("code=%d adds=%v", rec.Code, p.refreshes.snapshot())
		}
	})

	t.Run("valid_waiter_counts_nothing", func(t *testing.T) {
		// Lock held elsewhere, token still valid: served now, and the
		// holder's own rotation is the one counted event.
		fc := newStubCache()
		fc.locks[session.RefreshKey("refresh-1")] = "someone-else"
		_, p, rec := drive(t, fc, &stubAuth{}, 10*time.Second)
		if rec.Code != http.StatusOK || p.refreshes.count() != 0 {
			t.Fatalf("code=%d adds=%v", rec.Code, p.refreshes.snapshot())
		}
	})
}

func TestUnitCacheLookupMetric_MeAndRecs(t *testing.T) {
	cacheOutcome := func(cache, o string) []attribute.KeyValue {
		return []attribute.KeyValue{attribute.String("cache", cache), attribute.String("outcome", o)}
	}

	newMeHandlers := func(t *testing.T, fc *stubCache) (*Handlers, string) {
		t.Helper()
		uid := uuid.New()
		h := newTestHandlers(t, fc, &stubAuthFull{})
		h.users = &stubUsersFull{user: userapi.User{
			Id: uid, Email: "alice@example.test", Handle: "alice", Roles: []userapi.UserRoles{"user"},
		}}
		return h, uid.String()
	}

	t.Run("me_miss_then_hit", func(t *testing.T) {
		h, sub := newMeHandlers(t, newStubCache())
		p := probeMetrics(h)
		access := mintAccess(t, sub, "j1", time.Now().Add(5*time.Minute))
		router := newRouterFor(t, h)
		for range 2 {
			r := httptest.NewRequest(http.MethodGet, "/api/me", nil)
			r.AddCookie(sealedCookie(t, h, access, "r1"))
			rec := httptest.NewRecorder()
			router.ServeHTTP(rec, r)
			if rec.Code != http.StatusOK {
				t.Fatalf("code = %d", rec.Code)
			}
		}
		if p.lookups.total(cacheOutcome("me", "miss")...) != 1 || p.lookups.total(cacheOutcome("me", "hit")...) != 1 {
			t.Fatalf("lookups = %v", p.lookups.snapshot())
		}
	})

	t.Run("me_read_error_counts_miss", func(t *testing.T) {
		fc := newStubCache()
		fc.err = errors.New("valkey down")
		h, sub := newMeHandlers(t, fc)
		p := probeMetrics(h)
		access := mintAccess(t, sub, "j1", time.Now().Add(5*time.Minute))
		r := httptest.NewRequest(http.MethodGet, "/api/me", nil)
		r.AddCookie(sealedCookie(t, h, access, "r1"))
		rec := httptest.NewRecorder()
		newRouterFor(t, h).ServeHTTP(rec, r)
		if rec.Code != http.StatusOK {
			t.Fatalf("fail-open compose: code = %d", rec.Code)
		}
		assertOnly(t, p.lookups, cacheOutcome("me", "miss")...)
	})

	newRecsHandlers := func(t *testing.T, fc *stubCache) (*Handlers, *testEnv) {
		t.Helper()
		h := newTestHandlers(t, fc, &stubAuthFull{})
		h.collection = &stubCollection{library: func(context.Context, string) (collectionapi.LibrarySummary, error) {
			return collectionapi.LibrarySummary{Library: []collectionapi.LibraryGame{}}, nil
		}}
		h.enrichment = &stubEnrichment{score: func(context.Context, string, enrichapi.ScoreRequest) ([]byte, bool, error) {
			return []byte(`{"degraded":false,"recommendations":[]}`), false, nil
		}}
		access := mintAccess(t, uuid.New().String(), "j1", time.Now().Add(5*time.Minute))
		return h, &testEnv{cookie: sealedCookie(t, h, access, "r1"), sessionAccessToken: access}
	}

	t.Run("recs_miss_then_hit", func(t *testing.T) {
		h, env := newRecsHandlers(t, newStubCache())
		p := probeMetrics(h)
		for range 2 {
			if rec := doAuthed(t, h, env, http.MethodGet, "/api/recommendations"); rec.Code != http.StatusOK {
				t.Fatalf("code = %d", rec.Code)
			}
		}
		if p.lookups.total(cacheOutcome("recs", "miss")...) != 1 || p.lookups.total(cacheOutcome("recs", "hit")...) != 1 {
			t.Fatalf("lookups = %v", p.lookups.snapshot())
		}
	})

	t.Run("recs_read_error_counts_miss", func(t *testing.T) {
		fc := newStubCache()
		fc.err = errors.New("valkey down")
		h, env := newRecsHandlers(t, fc)
		p := probeMetrics(h)
		if rec := doAuthed(t, h, env, http.MethodGet, "/api/recommendations"); rec.Code != http.StatusOK {
			t.Fatalf("fail-open compose: code = %d", rec.Code)
		}
		assertOnly(t, p.lookups, cacheOutcome("recs", "miss")...)
	})
}

func TestUnitProxyTraces_RelayFailureLogsWarn(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))
	upstream.Close() // closed before any request: dialing it must fail

	h, env := newTestHandlersForOtlp(t, upstream.URL)
	var logBuf bytes.Buffer
	h.logger = slog.New(slog.NewTextHandler(&logBuf, nil))
	req := httptest.NewRequest(http.MethodPost, "/api/otlp/v1/traces", strings.NewReader(`{}`))
	req.AddCookie(env.cookie)
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	newRouterFor(t, h).ServeHTTP(rec, req)
	if rec.Code != http.StatusBadGateway {
		t.Fatalf("code = %d", rec.Code)
	}
	line := logBuf.String()
	if !strings.Contains(line, "browser telemetry relay failed") || !strings.Contains(line, "err=") {
		t.Fatalf("relay warn line missing: %q", line)
	}
}

func TestUnitProxyMetrics_RelayFailureLogsWarn(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))
	upstream.Close() // closed before any request: dialing it must fail

	h, env := newTestHandlersForOtlp(t, upstream.URL)
	var logBuf bytes.Buffer
	h.logger = slog.New(slog.NewTextHandler(&logBuf, nil))
	req := httptest.NewRequest(http.MethodPost, "/api/otlp/v1/metrics", strings.NewReader(`{}`))
	req.AddCookie(env.cookie)
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	newRouterFor(t, h).ServeHTTP(rec, req)
	if rec.Code != http.StatusBadGateway {
		t.Fatalf("code = %d", rec.Code)
	}
	line := logBuf.String()
	if !strings.Contains(line, "browser telemetry relay failed") || !strings.Contains(line, "err=") {
		t.Fatalf("relay warn line missing: %q", line)
	}
}

// TestUnitNew_NilLoggerDoesNotPanic pins the constructor's
// tolerate-nil idiom (shared across services): a caller that leaves
// Options.Logger nil gets slog.Default() instead of a panic on the
// first log call.
func TestUnitNew_NilLoggerDoesNotPanic(t *testing.T) {
	h := New(nil, nil, nil, nil, nil, nil, nil, Options{})
	if h == nil {
		t.Fatal("New returned nil")
	}
	if h.logger == nil {
		t.Fatal("logger must default to slog.Default(), not stay nil")
	}
}
