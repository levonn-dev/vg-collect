package oidc

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"maps"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	"github.com/golang-jwt/jwt/v5"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/metric"
)

// ProviderError wraps upstream identity-provider failures (network,
// non-200, malformed responses) so handlers can map them to 502.
type ProviderError struct {
	Op     string
	Status int // 0 when the request never completed
	Err    error
}

func (e *ProviderError) Error() string {
	if e.Status != 0 {
		return fmt.Sprintf("oidc: %s: provider status %d", e.Op, e.Status)
	}
	return fmt.Sprintf("oidc: %s: %v", e.Op, e.Err)
}

func (e *ProviderError) Unwrap() error { return e.Err }

// doJSON runs one provider HTTP round trip and decodes a JSON response
// into out. Every failure mode (request construction, transport, a
// non-200 status, or a malformed body) returns *ProviderError, so
// fetchDiscovery, redeemCode, and the JWKS refetch blame the identity
// provider the same way, and OauthCallback's errors.As sees one type
// regardless of which leg of the OIDC dance failed. Decoded-value
// checks (issuer match, a non-empty id_token) are the caller's job,
// not a transport failure.
func doJSON(ctx context.Context, hc *http.Client, method, url string, body io.Reader, headers map[string]string, op string, out any) *ProviderError {
	req, err := http.NewRequestWithContext(ctx, method, url, body)
	if err != nil {
		return &ProviderError{Op: op, Err: err}
	}
	for k, v := range headers {
		req.Header.Set(k, v)
	}
	resp, err := hc.Do(req)
	if err != nil {
		return &ProviderError{Op: op, Err: err}
	}
	defer func() {
		_ = resp.Body.Close()
	}()
	if resp.StatusCode != http.StatusOK {
		return &ProviderError{Op: op, Status: resp.StatusCode}
	}
	if err := json.NewDecoder(resp.Body).Decode(out); err != nil {
		return &ProviderError{Op: op, Err: err}
	}
	return nil
}

// RPConfig parameterizes one generic OIDC relying party. Provider
// quirks (scopes, extra authorize params) live in the per-provider
// constructors, not in this code path.
type RPConfig struct {
	Name            string
	IssuerURL       string // discovery at IssuerURL + /.well-known/openid-configuration
	ClientID        string
	ClientSecret    string
	RedirectURL     string
	Scopes          []string
	ExtraAuthParams url.Values
}

// RP is a hand-rolled OIDC relying party: discovery, authorization-code
// flow with PKCE, and full ID-token verification.
type RP struct {
	cfg         RPConfig
	hc          *http.Client
	keys        *rsaKeyCache
	reqDuration metric.Float64Histogram

	mu   sync.Mutex
	disc *discovery
}

// Provider round-trip op label values. ProviderError.Op stays prose
// ("token exchange"); these are the bounded metric spellings.
const (
	opDiscovery     = "discovery"
	opTokenExchange = "token_exchange"
	opJWKS          = "jwks"
)

// recordProviderRequest records one relying-party HTTP round trip on
// the shared histogram. Every label is a bounded set: provider is a
// configured RP name, op one of the constants above, and outcome
// collapses to ok|error.
func recordProviderRequest(ctx context.Context, hist metric.Float64Histogram, provider, op string, start time.Time, failed bool) {
	if hist == nil {
		return
	}
	outcome := "ok"
	if failed {
		outcome = "error"
	}
	hist.Record(ctx, time.Since(start).Seconds(), metric.WithAttributes(
		attribute.String("provider", provider),
		attribute.String("op", op),
		attribute.String("outcome", outcome)))
}

type discovery struct {
	Issuer                string `json:"issuer"`
	AuthorizationEndpoint string `json:"authorization_endpoint"`
	TokenEndpoint         string `json:"token_endpoint"`
	JWKSURI               string `json:"jwks_uri"`
}

// defaultJWKSRefetch bounds how often an unknown provider kid forces a
// JWKS refetch: fast enough to pick up provider key rotation, throttled
// so a flood of unknown kids cannot hammer the provider. Mirrors the
// jwtauth validator's refetch window.
const defaultJWKSRefetch = 30 * time.Second

// NewRP builds a relying party; hc nil means a 10s-timeout default.
func NewRP(cfg RPConfig, hc *http.Client) *RP {
	return NewRPWithRefetchInterval(cfg, hc, defaultJWKSRefetch)
}

// NewRPWithRefetchInterval is NewRP with a custom minimum JWKS refetch
// interval. Pass 0 to refetch on every unknown kid (used by tests).
func NewRPWithRefetchInterval(cfg RPConfig, hc *http.Client, jwksRefetch time.Duration) *RP {
	if hc == nil {
		hc = &http.Client{Timeout: 10 * time.Second}
	}
	// Best-effort, like every domain instrument: a registration failure
	// logs and the record sites no-op. Both real providers create the
	// same instrument (the SDK deduplicates); the dev provider has no
	// RP, so it never appears in this histogram.
	hist, err := otel.Meter("github.com/levonn-dev/vgkeep/services/auth").
		Float64Histogram("vg.auth.provider.request.duration",
			metric.WithDescription("Wall time of relying-party round trips to the identity provider"),
			metric.WithUnit("s"))
	if err != nil {
		slog.Error("provider request histogram unavailable", "err", err)
	}
	return &RP{cfg: cfg, hc: hc, reqDuration: hist,
		keys: newRSAKeyCache(hc, jwksRefetch, hist, cfg.Name)}
}

func (p *RP) Name() string { return p.cfg.Name }

// discover fetches and caches the provider metadata. Lazy (first use,
// not boot) so a provider outage cannot crash-loop the whole service;
// only successful fetches are cached. Only the fetch is measured; the
// cached fast path records nothing.
func (p *RP) discover(ctx context.Context) (*discovery, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.disc != nil {
		return p.disc, nil
	}
	start := time.Now()
	d, err := p.fetchDiscovery(ctx)
	recordProviderRequest(ctx, p.reqDuration, p.cfg.Name, opDiscovery, start, err != nil)
	if err != nil {
		return nil, err
	}
	p.disc = d
	return p.disc, nil
}

// fetchDiscovery performs the discovery round trip and validates that
// the document declares the issuer we were configured to talk to
// (mix-up defense).
func (p *RP) fetchDiscovery(ctx context.Context) (*discovery, error) {
	u := strings.TrimSuffix(p.cfg.IssuerURL, "/") + "/.well-known/openid-configuration"
	var d discovery
	if err := doJSON(ctx, p.hc, http.MethodGet, u, nil, nil, "discovery", &d); err != nil {
		return nil, err
	}
	if strings.TrimSuffix(d.Issuer, "/") != strings.TrimSuffix(p.cfg.IssuerURL, "/") {
		return nil, &ProviderError{Op: "discovery",
			Err: fmt.Errorf("issuer mismatch: document says %q", d.Issuer)}
	}
	return &d, nil
}

// AuthorizeURL builds the provider redirect for the code flow.
func (p *RP) AuthorizeURL(ctx context.Context, state, nonce, challenge string) (string, error) {
	d, err := p.discover(ctx)
	if err != nil {
		return "", err
	}
	q := url.Values{
		"response_type":         {"code"},
		"client_id":             {p.cfg.ClientID},
		"redirect_uri":          {p.cfg.RedirectURL},
		"scope":                 {strings.Join(p.cfg.Scopes, " ")},
		"state":                 {state},
		"nonce":                 {nonce},
		"code_challenge":        {challenge},
		"code_challenge_method": {"S256"},
	}
	maps.Copy(q, p.cfg.ExtraAuthParams)
	return d.AuthorizationEndpoint + "?" + q.Encode(), nil
}

// Exchange redeems the code (client_secret_post + PKCE verifier) and
// fully verifies the ID token: RS256 signature against the provider
// JWKS, issuer, audience = client_id, required expiry with 30s leeway,
// and the nonce binding it to the login that started the flow. Only
// the token-endpoint round trip is measured here; verification is our
// own work (its JWKS fetch, when one happens, records op=jwks).
func (p *RP) Exchange(ctx context.Context, code, verifier, nonce string) (IDClaims, error) {
	d, err := p.discover(ctx)
	if err != nil {
		return IDClaims{}, err
	}
	start := time.Now()
	idToken, err := p.redeemCode(ctx, d, code, verifier)
	recordProviderRequest(ctx, p.reqDuration, p.cfg.Name, opTokenExchange, start, err != nil)
	if err != nil {
		return IDClaims{}, err
	}
	return p.verifyIDToken(ctx, d, idToken, nonce)
}

// redeemCode runs the token-endpoint POST and returns the raw ID token.
func (p *RP) redeemCode(ctx context.Context, d *discovery, code, verifier string) (string, error) {
	form := url.Values{
		"grant_type":    {"authorization_code"},
		"code":          {code},
		"redirect_uri":  {p.cfg.RedirectURL},
		"code_verifier": {verifier},
		"client_id":     {p.cfg.ClientID},
		"client_secret": {p.cfg.ClientSecret},
	}
	var body struct {
		IDToken string `json:"id_token"`
	}
	headers := map[string]string{"Content-Type": "application/x-www-form-urlencoded"}
	if err := doJSON(ctx, p.hc, http.MethodPost, d.TokenEndpoint,
		strings.NewReader(form.Encode()), headers, "token exchange", &body); err != nil {
		return "", err
	}
	if body.IDToken == "" {
		return "", &ProviderError{Op: "token exchange", Err: errors.New("no id_token in response")}
	}
	return body.IDToken, nil
}

func (p *RP) verifyIDToken(ctx context.Context, d *discovery, raw, nonce string) (IDClaims, error) {
	mc := jwt.MapClaims{}
	_, err := jwt.ParseWithClaims(raw, mc, func(t *jwt.Token) (any, error) {
		kid, _ := t.Header["kid"].(string)
		if kid == "" {
			return nil, errors.New("oidc: id token missing kid")
		}
		return p.keys.get(ctx, d.JWKSURI, kid)
	},
		jwt.WithValidMethods([]string{"RS256"}),
		jwt.WithIssuer(d.Issuer),
		jwt.WithAudience(p.cfg.ClientID),
		jwt.WithExpirationRequired(),
		jwt.WithLeeway(30*time.Second),
	)
	if err != nil {
		return IDClaims{}, fmt.Errorf("oidc: verify id token: %w", err)
	}
	if got, _ := mc["nonce"].(string); got != nonce {
		return IDClaims{}, errors.New("oidc: nonce mismatch")
	}
	sub, err := mc.GetSubject()
	if err != nil || sub == "" {
		return IDClaims{}, errors.New("oidc: id token missing sub")
	}
	out := IDClaims{Subject: sub}
	out.Email, _ = mc["email"].(string)
	out.EmailVerified, _ = mc["email_verified"].(bool)
	out.AvatarURL, _ = mc["picture"].(string)
	// Display-name preference: standard "name", then Twitch's
	// "preferred_username", then the email local part.
	if v, _ := mc["name"].(string); v != "" {
		out.DisplayName = v
	} else if v, _ := mc["preferred_username"].(string); v != "" {
		out.DisplayName = v
	} else if i := strings.IndexByte(out.Email, '@'); i > 0 {
		out.DisplayName = out.Email[:i]
	}
	return out, nil
}
