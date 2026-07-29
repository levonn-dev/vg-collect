package server_test

// Direct tests for the domain telemetry added to the handlers: outcome
// classification and attribute derivation for vg.auth.login.outcomes
// and vg.auth.token.refreshes, the vg.auth.signing_keys.active gauge
// callback, and the structured log events behind problem responses.
// They drive the real handlers with the stubs from handlers_test.go
// and read the recorded metrics back through an SDK manual reader.

import (
	"bytes"
	"context"
	"encoding/json"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"
	"time"

	"github.com/google/uuid"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	sdkmetric "go.opentelemetry.io/otel/sdk/metric"
	"go.opentelemetry.io/otel/sdk/metric/metricdata"

	"github.com/levonn-dev/vgkeep/libs/go/jwtauth"
	"github.com/levonn-dev/vgkeep/services/auth/internal/gen/api"
	"github.com/levonn-dev/vgkeep/services/auth/internal/oidc"
	"github.com/levonn-dev/vgkeep/services/auth/internal/server"
	"github.com/levonn-dev/vgkeep/services/auth/internal/store"
	"github.com/levonn-dev/vgkeep/services/auth/internal/userclient"
)

// TestMain pins the global meter provider to a real (readerless) SDK
// provider before any Handlers is built. Without it the default
// delegating provider would queue every instrument and callback
// registered by earlier tests and replay them into the first per-test
// provider installMeterReader installs, so its Collect would fire
// signing-key callbacks against stubs those tests never wired.
func TestMain(m *testing.M) {
	otel.SetMeterProvider(sdkmetric.NewMeterProvider())
	os.Exit(m.Run())
}

// installMeterReader routes instruments created during this test into
// a manual reader; cleanup swaps in a fresh readerless provider so
// later tests never collect this test's callbacks.
func installMeterReader(t *testing.T) *sdkmetric.ManualReader {
	t.Helper()
	reader := sdkmetric.NewManualReader()
	otel.SetMeterProvider(sdkmetric.NewMeterProvider(sdkmetric.WithReader(reader)))
	t.Cleanup(func() { otel.SetMeterProvider(sdkmetric.NewMeterProvider()) })
	return reader
}

func collectMetrics(t *testing.T, reader *sdkmetric.ManualReader) metricdata.ResourceMetrics {
	t.Helper()
	var rm metricdata.ResourceMetrics
	if err := reader.Collect(context.Background(), &rm); err != nil {
		t.Fatalf("collect: %v", err)
	}
	return rm
}

func metricByName(rm metricdata.ResourceMetrics, name string) (metricdata.Metrics, bool) {
	for _, sm := range rm.ScopeMetrics {
		for _, m := range sm.Metrics {
			if m.Name == name {
				return m, true
			}
		}
	}
	return metricdata.Metrics{}, false
}

// sumPoints returns the named Int64Counter's data points (nil when the
// instrument recorded nothing).
func sumPoints(t *testing.T, reader *sdkmetric.ManualReader, name string) []metricdata.DataPoint[int64] {
	t.Helper()
	m, ok := metricByName(collectMetrics(t, reader), name)
	if !ok {
		return nil
	}
	sum, ok := m.Data.(metricdata.Sum[int64])
	if !ok {
		t.Fatalf("%s data = %T, want Sum[int64]", name, m.Data)
	}
	return sum.DataPoints
}

// wantSingleCount asserts the counter recorded exactly one data point:
// value 1 under exactly the given attributes.
func wantSingleCount(t *testing.T, pts []metricdata.DataPoint[int64], attrs ...attribute.KeyValue) {
	t.Helper()
	if len(pts) != 1 {
		t.Fatalf("data points = %d, want 1 (%+v)", len(pts), pts)
	}
	want := attribute.NewSet(attrs...)
	if pts[0].Value != 1 || !pts[0].Attributes.Equals(&want) {
		t.Fatalf("point = value %d attrs %s, want value 1 attrs %s",
			pts[0].Value, pts[0].Attributes.Encoded(attribute.DefaultEncoder()),
			want.Encoded(attribute.DefaultEncoder()))
	}
}

func loginAttrs(provider, flow, outcome string) []attribute.KeyValue {
	return []attribute.KeyValue{
		attribute.String("provider", provider),
		attribute.String("flow", flow),
		attribute.String("outcome", outcome),
	}
}

// signingKeysOK satisfies the gauge callback during counter tests
// (Collect invokes it on every Handlers built while a reader is
// installed).
func signingKeysOK(context.Context) ([]store.SigningKey, error) {
	return []store.SigningKey{{Kid: "k1"}}, nil
}

// googleState makes ConsumeState hand back a pending google dance;
// linkTo non-nil marks it a link flow.
func googleState(linkTo *uuid.UUID) func(context.Context, string) (store.AuthState, error) {
	return func(context.Context, string) (store.AuthState, error) {
		return store.AuthState{State: "s1", PKCEVerifier: "v1", Nonce: "n1",
			Provider: "google", ExpiresAt: time.Now().Add(time.Minute), LinkUserID: linkTo}, nil
	}
}

func TestLoginOutcomeMetric(t *testing.T) {
	linkUser := uuid.New()
	callbackBody := api.CallbackRequest{Code: "c1", State: "s1"}

	cases := []struct {
		name       string
		build      func() *server.Handlers
		call       func(*server.Handlers, http.ResponseWriter, *http.Request)
		body       any
		wantStatus int
		// wantOutcome "" means the terminal must not be counted.
		wantProvider, wantFlow, wantOutcome string
	}{
		{
			name: "callback provider error is provider_error",
			build: func() *server.Handlers {
				st := &stubStore{consumeState: googleState(nil), activeSigningKeys: signingKeysOK}
				p := &stubProvider{name: "google", exchange: func(context.Context, string, string, string) (oidc.IDClaims, error) {
					return oidc.IDClaims{}, &oidc.ProviderError{Op: "token exchange", Status: 502}
				}}
				return newUnit(st, unitMinter(), &stubUserService{}, providerMap(p), &stubVerifier{}, false)
			},
			call:         (*server.Handlers).OauthCallback,
			body:         callbackBody,
			wantStatus:   http.StatusBadGateway,
			wantProvider: "google", wantFlow: "login", wantOutcome: "provider_error",
		},
		{
			name: "callback verification failure is rejected",
			build: func() *server.Handlers {
				st := &stubStore{consumeState: googleState(nil), activeSigningKeys: signingKeysOK}
				p := &stubProvider{name: "google", exchange: func(context.Context, string, string, string) (oidc.IDClaims, error) {
					return oidc.IDClaims{}, errStub
				}}
				return newUnit(st, unitMinter(), &stubUserService{}, providerMap(p), &stubVerifier{}, false)
			},
			call:         (*server.Handlers).OauthCallback,
			body:         callbackBody,
			wantStatus:   http.StatusBadRequest,
			wantProvider: "google", wantFlow: "login", wantOutcome: "rejected",
		},
		{
			name: "link-flow callback carries flow=link",
			build: func() *server.Handlers {
				st := &stubStore{consumeState: googleState(&linkUser), activeSigningKeys: signingKeysOK}
				p := &stubProvider{name: "google", exchange: func(context.Context, string, string, string) (oidc.IDClaims, error) {
					return oidc.IDClaims{}, &oidc.ProviderError{Op: "token exchange", Err: errStub}
				}}
				return newUnit(st, unitMinter(), &stubUserService{}, providerMap(p), &stubVerifier{}, false)
			},
			call:         (*server.Handlers).OauthCallback,
			body:         callbackBody,
			wantStatus:   http.StatusBadGateway,
			wantProvider: "google", wantFlow: "link", wantOutcome: "provider_error",
		},
		{
			name: "completed login is success",
			build: func() *server.Handlers {
				st := &stubStore{
					consumeState: googleState(nil),
					resolveIdentity: func(context.Context, string, string, string) (store.Identity, error) {
						return store.Identity{}, store.ErrIdentityNotFound
					},
					bindIdentity:      func(context.Context, string, string, string, uuid.UUID) error { return nil },
					createSession:     func(context.Context, string, uuid.UUID, string, time.Time) error { return nil },
					activeSigningKeys: signingKeysOK,
				}
				p := &stubProvider{name: "google", exchange: func(context.Context, string, string, string) (oidc.IDClaims, error) {
					return verifiedClaims(), nil
				}}
				users := &stubUserService{upsert: func(context.Context, string, string, *string, string) (userclient.User, error) {
					return upsertedUser(), nil
				}}
				return newUnit(st, unitMinter(), users, providerMap(p), &stubVerifier{}, false)
			},
			call:         (*server.Handlers).OauthCallback,
			body:         callbackBody,
			wantStatus:   http.StatusOK,
			wantProvider: "google", wantFlow: "login", wantOutcome: "success",
		},
		{
			name: "unverified email is rejected",
			build: func() *server.Handlers {
				st := &stubStore{consumeState: googleState(nil), activeSigningKeys: signingKeysOK}
				p := &stubProvider{name: "google", exchange: func(context.Context, string, string, string) (oidc.IDClaims, error) {
					return oidc.IDClaims{Subject: "s", Email: "a@example.com", EmailVerified: false}, nil
				}}
				return newUnit(st, unitMinter(), &stubUserService{}, providerMap(p), &stubVerifier{}, false)
			},
			call:         (*server.Handlers).OauthCallback,
			body:         callbackBody,
			wantStatus:   http.StatusForbidden,
			wantProvider: "google", wantFlow: "login", wantOutcome: "rejected",
		},
		{
			name: "user service upsert failure is upstream_error",
			build: func() *server.Handlers {
				st := &stubStore{
					consumeState: googleState(nil),
					resolveIdentity: func(context.Context, string, string, string) (store.Identity, error) {
						return store.Identity{}, store.ErrIdentityNotFound
					},
					activeSigningKeys: signingKeysOK,
				}
				p := &stubProvider{name: "google", exchange: func(context.Context, string, string, string) (oidc.IDClaims, error) {
					return verifiedClaims(), nil
				}}
				users := &stubUserService{upsert: func(context.Context, string, string, *string, string) (userclient.User, error) {
					return userclient.User{}, errStub
				}}
				return newUnit(st, unitMinter(), users, providerMap(p), &stubVerifier{}, false)
			},
			call:         (*server.Handlers).OauthCallback,
			body:         callbackBody,
			wantStatus:   http.StatusBadGateway,
			wantProvider: "google", wantFlow: "login", wantOutcome: "upstream_error",
		},
		{
			name: "identity bind failure is internal_error",
			build: func() *server.Handlers {
				st := &stubStore{
					consumeState: googleState(nil),
					resolveIdentity: func(context.Context, string, string, string) (store.Identity, error) {
						return store.Identity{}, store.ErrIdentityNotFound
					},
					bindIdentity:      func(context.Context, string, string, string, uuid.UUID) error { return errStub },
					activeSigningKeys: signingKeysOK,
				}
				p := &stubProvider{name: "google", exchange: func(context.Context, string, string, string) (oidc.IDClaims, error) {
					return verifiedClaims(), nil
				}}
				users := &stubUserService{upsert: func(context.Context, string, string, *string, string) (userclient.User, error) {
					return upsertedUser(), nil
				}}
				return newUnit(st, unitMinter(), users, providerMap(p), &stubVerifier{}, false)
			},
			call:         (*server.Handlers).OauthCallback,
			body:         callbackBody,
			wantStatus:   http.StatusInternalServerError,
			wantProvider: "google", wantFlow: "login", wantOutcome: "internal_error",
		},
		{
			name: "mint failure is internal_error",
			build: func() *server.Handlers {
				st := &stubStore{
					consumeState: googleState(nil),
					resolveIdentity: func(context.Context, string, string, string) (store.Identity, error) {
						return store.Identity{}, store.ErrIdentityNotFound
					},
					bindIdentity:      func(context.Context, string, string, string, uuid.UUID) error { return nil },
					activeSigningKeys: signingKeysOK,
				}
				p := &stubProvider{name: "google", exchange: func(context.Context, string, string, string) (oidc.IDClaims, error) {
					return verifiedClaims(), nil
				}}
				users := &stubUserService{upsert: func(context.Context, string, string, *string, string) (userclient.User, error) {
					return upsertedUser(), nil
				}}
				return newUnit(st, &stubMinter{mintErr: errStub, ttl: 5 * time.Minute}, users, providerMap(p), &stubVerifier{}, false)
			},
			call:         (*server.Handlers).OauthCallback,
			body:         callbackBody,
			wantStatus:   http.StatusInternalServerError,
			wantProvider: "google", wantFlow: "login", wantOutcome: "internal_error",
		},
		{
			name: "dev unknown fixture is rejected on the login flow",
			build: func() *server.Handlers {
				return newUnit(&stubStore{activeSigningKeys: signingKeysOK}, unitMinter(),
					&stubUserService{}, nil, &stubVerifier{}, true)
			},
			call:         (*server.Handlers).DevToken,
			body:         api.DevTokenRequest{User: "nobody"},
			wantStatus:   http.StatusBadRequest,
			wantProvider: "dev", wantFlow: "login", wantOutcome: "rejected",
		},
		{
			name: "link identity conflict is rejected on the link flow",
			build: func() *server.Handlers {
				linked := linkUser
				st := &stubStore{
					bindIdentity: func(context.Context, string, string, string, uuid.UUID) error {
						return store.ErrIdentityTaken
					},
					activeSigningKeys: signingKeysOK,
				}
				users := &stubUserService{get: func(_ context.Context, id uuid.UUID) (userclient.User, error) {
					return userclient.User{ID: id, Roles: []string{"user"}}, nil
				}}
				v := &stubVerifier{validate: func(context.Context, string) (jwtauth.Claims, error) {
					return jwtauth.Claims{Subject: linked.String(), Roles: []string{"user"}}, nil
				}}
				return newUnit(st, unitMinter(), users, nil, v, true)
			},
			call:         (*server.Handlers).DevLink,
			body:         api.DevLinkRequest{User: "alice"},
			wantStatus:   http.StatusConflict,
			wantProvider: "dev", wantFlow: "link", wantOutcome: "rejected",
		},
		{
			name: "link target gone is rejected on the link flow",
			build: func() *server.Handlers {
				linked := linkUser
				st := &stubStore{activeSigningKeys: signingKeysOK}
				users := &stubUserService{get: func(context.Context, uuid.UUID) (userclient.User, error) {
					return userclient.User{}, userclient.ErrUserNotFound
				}}
				v := &stubVerifier{validate: func(context.Context, string) (jwtauth.Claims, error) {
					return jwtauth.Claims{Subject: linked.String(), Roles: []string{"user"}}, nil
				}}
				return newUnit(st, unitMinter(), users, nil, v, true)
			},
			call:         (*server.Handlers).DevLink,
			body:         api.DevLinkRequest{User: "alice"},
			wantStatus:   http.StatusBadRequest,
			wantProvider: "dev", wantFlow: "link", wantOutcome: "rejected",
		},
		{
			name: "link user lookup failure is upstream_error",
			build: func() *server.Handlers {
				linked := linkUser
				st := &stubStore{activeSigningKeys: signingKeysOK}
				users := &stubUserService{get: func(context.Context, uuid.UUID) (userclient.User, error) {
					return userclient.User{}, errStub
				}}
				v := &stubVerifier{validate: func(context.Context, string) (jwtauth.Claims, error) {
					return jwtauth.Claims{Subject: linked.String(), Roles: []string{"user"}}, nil
				}}
				return newUnit(st, unitMinter(), users, nil, v, true)
			},
			call:         (*server.Handlers).DevLink,
			body:         api.DevLinkRequest{User: "alice"},
			wantStatus:   http.StatusBadGateway,
			wantProvider: "dev", wantFlow: "link", wantOutcome: "upstream_error",
		},
		{
			name: "completed link is success on the link flow",
			build: func() *server.Handlers {
				linked := linkUser
				st := &stubStore{
					bindIdentity:      func(context.Context, string, string, string, uuid.UUID) error { return nil },
					createSession:     func(context.Context, string, uuid.UUID, string, time.Time) error { return nil },
					activeSigningKeys: signingKeysOK,
				}
				users := &stubUserService{get: func(_ context.Context, id uuid.UUID) (userclient.User, error) {
					return userclient.User{ID: id, Roles: []string{"user"}}, nil
				}}
				v := &stubVerifier{validate: func(context.Context, string) (jwtauth.Claims, error) {
					return jwtauth.Claims{Subject: linked.String(), Roles: []string{"user"}}, nil
				}}
				return newUnit(st, unitMinter(), users, nil, v, true)
			},
			call:         (*server.Handlers).DevLink,
			body:         api.DevLinkRequest{User: "alice"},
			wantStatus:   http.StatusOK,
			wantProvider: "dev", wantFlow: "link", wantOutcome: "success",
		},
		{
			name: "link session mint failure is internal_error",
			build: func() *server.Handlers {
				linked := linkUser
				st := &stubStore{
					bindIdentity:      func(context.Context, string, string, string, uuid.UUID) error { return nil },
					activeSigningKeys: signingKeysOK,
				}
				users := &stubUserService{get: func(_ context.Context, id uuid.UUID) (userclient.User, error) {
					return userclient.User{ID: id, Roles: []string{"user"}}, nil
				}}
				v := &stubVerifier{validate: func(context.Context, string) (jwtauth.Claims, error) {
					return jwtauth.Claims{Subject: linked.String(), Roles: []string{"user"}}, nil
				}}
				return newUnit(st, &stubMinter{mintErr: errStub, ttl: 5 * time.Minute}, users, nil, v, true)
			},
			call:         (*server.Handlers).DevLink,
			body:         api.DevLinkRequest{User: "alice"},
			wantStatus:   http.StatusInternalServerError,
			wantProvider: "dev", wantFlow: "link", wantOutcome: "internal_error",
		},
		{
			name: "malformed body is not counted",
			build: func() *server.Handlers {
				return newUnit(&stubStore{activeSigningKeys: signingKeysOK}, unitMinter(),
					&stubUserService{}, nil, &stubVerifier{}, false)
			},
			call:       (*server.Handlers).OauthStart,
			body:       "{not json",
			wantStatus: http.StatusBadRequest,
		},
		{
			name: "unknown provider is not counted",
			build: func() *server.Handlers {
				return newUnit(&stubStore{activeSigningKeys: signingKeysOK}, unitMinter(),
					&stubUserService{}, map[string]oidc.Provider{}, &stubVerifier{}, false)
			},
			call:       (*server.Handlers).OauthStart,
			body:       api.StartRequest{Provider: "twitch"},
			wantStatus: http.StatusBadRequest,
		},
		{
			name: "invalid state is not counted",
			build: func() *server.Handlers {
				st := &stubStore{
					consumeState: func(context.Context, string) (store.AuthState, error) {
						return store.AuthState{}, store.ErrStateNotFound
					},
					activeSigningKeys: signingKeysOK,
				}
				return newUnit(st, unitMinter(), &stubUserService{}, nil, &stubVerifier{}, false)
			},
			call:       (*server.Handlers).OauthCallback,
			body:       callbackBody,
			wantStatus: http.StatusBadRequest,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			reader := installMeterReader(t)
			h := tc.build()
			rec := httptest.NewRecorder()
			req := jsonReq(t, http.MethodPost, "/x", tc.body)
			req.Header.Set("Authorization", "Bearer irrelevant")
			tc.call(h, rec, req)
			if rec.Code != tc.wantStatus {
				t.Fatalf("status = %d, want %d (body %s)", rec.Code, tc.wantStatus, rec.Body.String())
			}
			pts := sumPoints(t, reader, "vg.auth.login.outcomes")
			if tc.wantOutcome == "" {
				if len(pts) != 0 {
					t.Fatalf("terminal must not be counted, got %+v", pts)
				}
				return
			}
			wantSingleCount(t, pts, loginAttrs(tc.wantProvider, tc.wantFlow, tc.wantOutcome)...)
		})
	}
}

func TestRefreshOutcomeMetric(t *testing.T) {
	uid := uuid.New()
	peekOK := func(context.Context, string) (store.Session, error) {
		return store.Session{UserID: uid}, nil
	}
	getOK := func(context.Context, uuid.UUID) (userclient.User, error) {
		return userclient.User{ID: uid, Roles: []string{"user"}}, nil
	}

	cases := []struct {
		name        string
		st          *stubStore
		users       *stubUserService
		minter      *stubMinter
		body        any
		wantStatus  int
		wantOutcome string // "" means not counted
	}{
		{
			name: "rotation success",
			st: &stubStore{peekSession: peekOK,
				rotate: func(context.Context, string, string, string, time.Duration) (store.RotateResult, error) {
					return store.RotateResult{UserID: uid, ExpiresAt: time.Now().Add(time.Hour)}, nil
				}},
			users: &stubUserService{get: getOK}, wantStatus: http.StatusOK, wantOutcome: "success",
		},
		{
			name: "unknown token is rejected",
			st: &stubStore{peekSession: func(context.Context, string) (store.Session, error) {
				return store.Session{}, store.ErrRefreshNotFound
			}},
			wantStatus: http.StatusUnauthorized, wantOutcome: "rejected",
		},
		{
			name: "revoked-family short-circuit is reuse_detected",
			st: &stubStore{peekSession: func(context.Context, string) (store.Session, error) {
				return store.Session{}, store.ErrRefreshRevoked
			}},
			wantStatus: http.StatusUnauthorized, wantOutcome: "reuse_detected",
		},
		{
			name: "session lookup failure is internal_error",
			st: &stubStore{peekSession: func(context.Context, string) (store.Session, error) {
				return store.Session{}, errStub
			}},
			wantStatus: http.StatusInternalServerError, wantOutcome: "internal_error",
		},
		{
			name: "user gone is rejected",
			st: &stubStore{peekSession: peekOK,
				revokeFamilyByToken: func(context.Context, string) error { return nil }},
			users: &stubUserService{get: func(context.Context, uuid.UUID) (userclient.User, error) {
				return userclient.User{}, userclient.ErrUserNotFound
			}},
			wantStatus: http.StatusUnauthorized, wantOutcome: "rejected",
		},
		{
			name: "user service down is upstream_error",
			st:   &stubStore{peekSession: peekOK},
			users: &stubUserService{get: func(context.Context, uuid.UUID) (userclient.User, error) {
				return userclient.User{}, errStub
			}},
			wantStatus: http.StatusServiceUnavailable, wantOutcome: "upstream_error",
		},
		{
			name:       "mint failure is internal_error",
			st:         &stubStore{peekSession: peekOK},
			users:      &stubUserService{get: getOK},
			minter:     &stubMinter{mintErr: errStub, ttl: 5 * time.Minute},
			wantStatus: http.StatusInternalServerError, wantOutcome: "internal_error",
		},
		{
			name: "rotation-time reuse is reuse_detected",
			st: &stubStore{peekSession: peekOK,
				rotate: func(context.Context, string, string, string, time.Duration) (store.RotateResult, error) {
					return store.RotateResult{}, &store.ReuseError{RevokedJTIs: []string{"j1", "j2"}}
				}},
			users: &stubUserService{get: getOK}, wantStatus: http.StatusUnauthorized, wantOutcome: "reuse_detected",
		},
		{
			name: "expired at rotation is rejected",
			st: &stubStore{peekSession: peekOK,
				rotate: func(context.Context, string, string, string, time.Duration) (store.RotateResult, error) {
					return store.RotateResult{}, store.ErrRefreshExpired
				}},
			users: &stubUserService{get: getOK}, wantStatus: http.StatusUnauthorized, wantOutcome: "rejected",
		},
		{
			name: "rotation failure is internal_error",
			st: &stubStore{peekSession: peekOK,
				rotate: func(context.Context, string, string, string, time.Duration) (store.RotateResult, error) {
					return store.RotateResult{}, errStub
				}},
			users: &stubUserService{get: getOK}, wantStatus: http.StatusInternalServerError, wantOutcome: "internal_error",
		},
		{
			name: "malformed body is not counted",
			st:   &stubStore{}, body: "{not json",
			wantStatus: http.StatusBadRequest, wantOutcome: "",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			reader := installMeterReader(t)
			tc.st.activeSigningKeys = signingKeysOK
			m := tc.minter
			if m == nil {
				m = unitMinter()
			}
			users := tc.users
			if users == nil {
				users = &stubUserService{}
			}
			h := newUnit(tc.st, m, users, nil, &stubVerifier{}, false)
			body := tc.body
			if body == nil {
				body = api.RefreshRequest{RefreshToken: "refresh-1"}
			}
			rec := httptest.NewRecorder()
			h.RefreshToken(rec, jsonReq(t, http.MethodPost, "/token/refresh", body))
			if rec.Code != tc.wantStatus {
				t.Fatalf("status = %d, want %d (body %s)", rec.Code, tc.wantStatus, rec.Body.String())
			}
			pts := sumPoints(t, reader, "vg.auth.token.refreshes")
			if tc.wantOutcome == "" {
				if len(pts) != 0 {
					t.Fatalf("terminal must not be counted, got %+v", pts)
				}
				return
			}
			wantSingleCount(t, pts, attribute.String("outcome", tc.wantOutcome))
		})
	}
}

func TestSigningKeysGauge(t *testing.T) {
	reader := installMeterReader(t)
	var sawDeadline bool
	st := &stubStore{activeSigningKeys: func(ctx context.Context) ([]store.SigningKey, error) {
		if d, ok := ctx.Deadline(); ok && time.Until(d) <= 5*time.Second {
			sawDeadline = true
		}
		return []store.SigningKey{{Kid: "old"}, {Kid: "new"}}, nil
	}}
	newUnit(st, unitMinter(), &stubUserService{}, nil, &stubVerifier{}, false)

	m, ok := metricByName(collectMetrics(t, reader), "vg.auth.signing_keys.active")
	if !ok {
		t.Fatal("gauge not collected")
	}
	g, ok := m.Data.(metricdata.Gauge[int64])
	if !ok {
		t.Fatalf("data = %T, want Gauge[int64]", m.Data)
	}
	if len(g.DataPoints) != 1 || g.DataPoints[0].Value != 2 {
		t.Fatalf("gauge points = %+v, want one point of 2", g.DataPoints)
	}
	if !sawDeadline {
		t.Fatal("signing-keys query must carry a bounded deadline")
	}
}

func TestSigningKeysGaugeStoreErrorIsAGap(t *testing.T) {
	reader := installMeterReader(t)
	st := &stubStore{activeSigningKeys: func(context.Context) ([]store.SigningKey, error) {
		return nil, errStub
	}}
	newUnit(st, unitMinter(), &stubUserService{}, nil, &stubVerifier{}, false)

	// Collect surfaces the callback error; what matters is that no
	// observation was recorded: a gap, never a false zero.
	var rm metricdata.ResourceMetrics
	_ = reader.Collect(context.Background(), &rm)
	if m, ok := metricByName(rm, "vg.auth.signing_keys.active"); ok {
		if g, isGauge := m.Data.(metricdata.Gauge[int64]); isGauge && len(g.DataPoints) != 0 {
			t.Fatalf("store error must record nothing, got %+v", g.DataPoints)
		}
	}
}

// --- log events ---

// captureLogs swaps the ambient slog default for a JSON handler over a
// buffer for this test (the handlers log through slog.Default, which
// production wires to the OTLP-fanout handler).
func captureLogs(t *testing.T) *bytes.Buffer {
	t.Helper()
	var buf bytes.Buffer
	prev := slog.Default()
	slog.SetDefault(slog.New(slog.NewJSONHandler(&buf, nil)))
	t.Cleanup(func() { slog.SetDefault(prev) })
	return &buf
}

// logLines decodes every captured line carrying the given msg.
func logLines(t *testing.T, buf *bytes.Buffer, msg string) []map[string]any {
	t.Helper()
	var out []map[string]any
	for _, raw := range bytes.Split(buf.Bytes(), []byte("\n")) {
		if len(raw) == 0 {
			continue
		}
		var line map[string]any
		if err := json.Unmarshal(raw, &line); err != nil {
			t.Fatalf("log line not JSON: %s", raw)
		}
		if line["msg"] == msg {
			out = append(out, line)
		}
	}
	return out
}

func TestReuseDetectedLogFields(t *testing.T) {
	uid := uuid.New()

	t.Run("revoked-family short-circuit has empty user_id", func(t *testing.T) {
		buf := captureLogs(t)
		// Runtime-random opaque value: proves by containment check that
		// no log line carries the presented token.
		presented := uuid.NewString()
		st := &stubStore{peekSession: func(context.Context, string) (store.Session, error) {
			return store.Session{}, store.ErrRefreshRevoked
		}}
		h := newUnit(st, unitMinter(), &stubUserService{}, nil, &stubVerifier{}, false)
		rec := httptest.NewRecorder()
		h.RefreshToken(rec, jsonReq(t, http.MethodPost, "/token/refresh",
			api.RefreshRequest{RefreshToken: presented}))

		lines := logLines(t, buf, "refresh reuse detected")
		if len(lines) != 1 {
			t.Fatalf("reuse warnings = %d, want 1", len(lines))
		}
		if lines[0]["level"] != "WARN" || lines[0]["user_id"] != "" || lines[0]["jti_count"] != float64(0) {
			t.Fatalf("line = %v, want WARN with empty user_id and jti_count 0", lines[0])
		}
		if bytes.Contains(buf.Bytes(), []byte(presented)) {
			t.Fatal("log output leaks the presented refresh token")
		}
	})

	t.Run("rotation-time detection carries user_id and jti_count", func(t *testing.T) {
		buf := captureLogs(t)
		presented := uuid.NewString()
		st := &stubStore{
			peekSession: func(context.Context, string) (store.Session, error) {
				return store.Session{UserID: uid}, nil
			},
			rotate: func(context.Context, string, string, string, time.Duration) (store.RotateResult, error) {
				return store.RotateResult{}, &store.ReuseError{RevokedJTIs: []string{"j1", "j2"}}
			},
		}
		users := &stubUserService{get: func(context.Context, uuid.UUID) (userclient.User, error) {
			return userclient.User{ID: uid, Roles: []string{"user"}}, nil
		}}
		h := newUnit(st, unitMinter(), users, nil, &stubVerifier{}, false)
		rec := httptest.NewRecorder()
		h.RefreshToken(rec, jsonReq(t, http.MethodPost, "/token/refresh",
			api.RefreshRequest{RefreshToken: presented}))

		lines := logLines(t, buf, "refresh reuse detected")
		if len(lines) != 1 {
			t.Fatalf("reuse warnings = %d, want 1", len(lines))
		}
		if lines[0]["level"] != "WARN" || lines[0]["user_id"] != uid.String() || lines[0]["jti_count"] != float64(2) {
			t.Fatalf("line = %v, want WARN with user_id %s and jti_count 2", lines[0], uid)
		}
		if bytes.Contains(buf.Bytes(), []byte(presented)) {
			t.Fatal("log output leaks the presented refresh token")
		}
	})
}

func TestErrorLogEvents(t *testing.T) {
	t.Run("auth store error names the op behind a 500", func(t *testing.T) {
		buf := captureLogs(t)
		st := &stubStore{createState: func(context.Context, store.AuthState) error { return errStub }}
		p := &stubProvider{name: "google"}
		h := newUnit(st, unitMinter(), &stubUserService{}, providerMap(p), &stubVerifier{}, false)
		rec := httptest.NewRecorder()
		h.OauthStart(rec, jsonReq(t, http.MethodPost, "/oauth/start", api.StartRequest{Provider: "google"}))

		lines := logLines(t, buf, "auth store error")
		if len(lines) != 1 || lines[0]["level"] != "ERROR" ||
			lines[0]["op"] != "create_state" || lines[0]["err"] != errStub.Error() {
			t.Fatalf("lines = %v, want one ERROR with op create_state", lines)
		}
	})

	t.Run("provider request failed names the provider behind a 502", func(t *testing.T) {
		buf := captureLogs(t)
		st := &stubStore{createState: func(context.Context, store.AuthState) error { return nil }}
		p := &stubProvider{name: "google", authorizeURL: func(context.Context, string, string, string) (string, error) {
			return "", &oidc.ProviderError{Op: "discovery", Status: 503}
		}}
		h := newUnit(st, unitMinter(), &stubUserService{}, providerMap(p), &stubVerifier{}, false)
		rec := httptest.NewRecorder()
		h.OauthStart(rec, jsonReq(t, http.MethodPost, "/oauth/start", api.StartRequest{Provider: "google"}))

		lines := logLines(t, buf, "provider request failed")
		if len(lines) != 1 || lines[0]["level"] != "ERROR" || lines[0]["provider"] != "google" {
			t.Fatalf("lines = %v, want one ERROR with provider google", lines)
		}
		// The ProviderError string must carry op and status for triage.
		if err, _ := lines[0]["err"].(string); err != "oidc: discovery: provider status 503" {
			t.Fatalf("err = %q, want the ProviderError op+status string", err)
		}
	})

	t.Run("link start store error logs op create_state", func(t *testing.T) {
		buf := captureLogs(t)
		caller := uuid.New()
		st := &stubStore{createState: func(context.Context, store.AuthState) error { return errStub }}
		p := &stubProvider{name: "google"}
		v := &stubVerifier{validate: func(context.Context, string) (jwtauth.Claims, error) {
			return jwtauth.Claims{Subject: caller.String(), Roles: []string{"user"}}, nil
		}}
		h := newUnit(st, unitMinter(), &stubUserService{}, providerMap(p), v, false)
		rec := httptest.NewRecorder()
		req := jsonReq(t, http.MethodPost, "/oauth/link/start", api.LinkStartRequest{Provider: "google"})
		req.Header.Set("Authorization", "Bearer irrelevant")
		h.OauthLinkStart(rec, req)
		if rec.Code != http.StatusInternalServerError {
			t.Fatalf("status = %d, want 500", rec.Code)
		}
		lines := logLines(t, buf, "auth store error")
		if len(lines) != 1 || lines[0]["op"] != "create_state" {
			t.Fatalf("lines = %v, want one with op create_state", lines)
		}
	})

	t.Run("link start provider failure logs the provider", func(t *testing.T) {
		buf := captureLogs(t)
		caller := uuid.New()
		st := &stubStore{createState: func(context.Context, store.AuthState) error { return nil }}
		p := &stubProvider{name: "google", authorizeURL: func(context.Context, string, string, string) (string, error) {
			return "", &oidc.ProviderError{Op: "discovery", Err: errStub}
		}}
		v := &stubVerifier{validate: func(context.Context, string) (jwtauth.Claims, error) {
			return jwtauth.Claims{Subject: caller.String(), Roles: []string{"user"}}, nil
		}}
		h := newUnit(st, unitMinter(), &stubUserService{}, providerMap(p), v, false)
		rec := httptest.NewRecorder()
		req := jsonReq(t, http.MethodPost, "/oauth/link/start", api.LinkStartRequest{Provider: "google"})
		req.Header.Set("Authorization", "Bearer irrelevant")
		h.OauthLinkStart(rec, req)
		if rec.Code != http.StatusBadGateway {
			t.Fatalf("status = %d, want 502", rec.Code)
		}
		lines := logLines(t, buf, "provider request failed")
		if len(lines) != 1 || lines[0]["provider"] != "google" {
			t.Fatalf("lines = %v, want one with provider google", lines)
		}
	})

	t.Run("user service unavailable names the op behind a refresh 503", func(t *testing.T) {
		buf := captureLogs(t)
		st := &stubStore{peekSession: func(context.Context, string) (store.Session, error) {
			return store.Session{UserID: uuid.New()}, nil
		}}
		users := &stubUserService{get: func(context.Context, uuid.UUID) (userclient.User, error) {
			return userclient.User{}, errStub
		}}
		h := newUnit(st, unitMinter(), users, nil, &stubVerifier{}, false)
		rec := httptest.NewRecorder()
		h.RefreshToken(rec, jsonReq(t, http.MethodPost, "/token/refresh",
			api.RefreshRequest{RefreshToken: "r"}))

		lines := logLines(t, buf, "user service unavailable")
		if len(lines) != 1 || lines[0]["level"] != "ERROR" ||
			lines[0]["op"] != "get" || lines[0]["err"] != errStub.Error() {
			t.Fatalf("lines = %v, want one ERROR with op get", lines)
		}
	})
}
