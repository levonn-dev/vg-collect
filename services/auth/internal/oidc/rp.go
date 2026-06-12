package oidc

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	"github.com/golang-jwt/jwt/v5"
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
	cfg  RPConfig
	hc   *http.Client
	keys *rsaKeyCache

	mu   sync.Mutex
	disc *discovery
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
	return &RP{cfg: cfg, hc: hc, keys: newRSAKeyCache(hc, jwksRefetch)}
}

func (p *RP) Name() string { return p.cfg.Name }

// discover fetches and caches the provider metadata. Lazy (first use,
// not boot) so a provider outage cannot crash-loop the whole service;
// only successful fetches are cached.
func (p *RP) discover(ctx context.Context) (*discovery, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.disc != nil {
		return p.disc, nil
	}
	u := strings.TrimSuffix(p.cfg.IssuerURL, "/") + "/.well-known/openid-configuration"
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	if err != nil {
		return nil, err
	}
	resp, err := p.hc.Do(req)
	if err != nil {
		return nil, &ProviderError{Op: "discovery", Err: err}
	}
	defer func() {
		_ = resp.Body.Close()
	}()
	if resp.StatusCode != http.StatusOK {
		return nil, &ProviderError{Op: "discovery", Status: resp.StatusCode}
	}
	var d discovery
	if err := json.NewDecoder(resp.Body).Decode(&d); err != nil {
		return nil, &ProviderError{Op: "discovery", Err: err}
	}
	// Mix-up defense: the document must declare the issuer we were
	// configured to talk to.
	if strings.TrimSuffix(d.Issuer, "/") != strings.TrimSuffix(p.cfg.IssuerURL, "/") {
		return nil, &ProviderError{Op: "discovery",
			Err: fmt.Errorf("issuer mismatch: document says %q", d.Issuer)}
	}
	p.disc = &d
	return p.disc, nil
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
	for k, vs := range p.cfg.ExtraAuthParams {
		q[k] = vs
	}
	return d.AuthorizationEndpoint + "?" + q.Encode(), nil
}

// Exchange redeems the code (client_secret_post + PKCE verifier) and
// fully verifies the ID token: RS256 signature against the provider
// JWKS, issuer, audience = client_id, required expiry with 30s leeway,
// and the nonce binding it to the login that started the flow.
func (p *RP) Exchange(ctx context.Context, code, verifier, nonce string) (IDClaims, error) {
	d, err := p.discover(ctx)
	if err != nil {
		return IDClaims{}, err
	}
	form := url.Values{
		"grant_type":    {"authorization_code"},
		"code":          {code},
		"redirect_uri":  {p.cfg.RedirectURL},
		"code_verifier": {verifier},
		"client_id":     {p.cfg.ClientID},
		"client_secret": {p.cfg.ClientSecret},
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, d.TokenEndpoint,
		strings.NewReader(form.Encode()))
	if err != nil {
		return IDClaims{}, err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	resp, err := p.hc.Do(req)
	if err != nil {
		return IDClaims{}, &ProviderError{Op: "token exchange", Err: err}
	}
	defer func() {
		_ = resp.Body.Close()
	}()
	if resp.StatusCode != http.StatusOK {
		return IDClaims{}, &ProviderError{Op: "token exchange", Status: resp.StatusCode}
	}
	var body struct {
		IDToken string `json:"id_token"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		return IDClaims{}, &ProviderError{Op: "token exchange", Err: err}
	}
	if body.IDToken == "" {
		return IDClaims{}, &ProviderError{Op: "token exchange", Err: errors.New("no id_token in response")}
	}
	return p.verifyIDToken(ctx, d, body.IDToken, nonce)
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
