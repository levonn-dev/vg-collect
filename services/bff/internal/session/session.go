// Package session defines the browser session: token pair sealed into the
// cookie, unverified claim extraction, and context plumbing. Claims are
// parsed WITHOUT signature verification on purpose: the cookie is AES-GCM
// sealed with the bff's own key, so anything that opens is something this
// service sealed; signature verification stays in the downstream services' jwtauth middleware.
package session

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"time"

	"github.com/golang-jwt/jwt/v5"
)

// Session is the sealed cookie payload.
type Session struct {
	AccessToken  string `json:"a"`
	RefreshToken string `json:"r"`
}

// Claims are the access-token fields the bff needs.
type Claims struct {
	Sub string
	JTI string
	Exp time.Time
}

// ParseClaims extracts sub/jti/exp without verifying the signature.
func ParseClaims(accessToken string) (Claims, error) {
	var mc jwt.MapClaims
	if _, _, err := jwt.NewParser().ParseUnverified(accessToken, &mc); err != nil {
		return Claims{}, fmt.Errorf("session: parse access token: %w", err)
	}
	sub, _ := mc["sub"].(string)
	jti, _ := mc["jti"].(string)
	exp, err := mc.GetExpirationTime()
	if err != nil || exp == nil || sub == "" || jti == "" {
		return Claims{}, errors.New("session: access token missing sub, jti, or exp")
	}
	return Claims{Sub: sub, JTI: jti, Exp: exp.Time}, nil
}

// RefreshKey derives the refresh-coordination cache key; hashed so the raw token never appears as a key.
func RefreshKey(refreshToken string) string {
	sum := sha256.Sum256([]byte(refreshToken))
	return hex.EncodeToString(sum[:])
}

type ctxKey struct{}

type ctxValue struct {
	Session Session
	Claims  Claims
}

// NewContext attaches the authenticated session to the request context.
func NewContext(ctx context.Context, s Session, c Claims) context.Context {
	return context.WithValue(ctx, ctxKey{}, ctxValue{Session: s, Claims: c})
}

// FromContext returns the session placed by the auth middleware.
func FromContext(ctx context.Context) (Session, Claims, bool) {
	v, ok := ctx.Value(ctxKey{}).(ctxValue)
	return v.Session, v.Claims, ok
}
