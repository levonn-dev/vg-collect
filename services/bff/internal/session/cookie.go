package session

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
)

// CookieName uses the __Host- prefix: the browser then guarantees the
// cookie is Secure, Path=/, and has no Domain, so a compromised sibling
// subdomain cannot set or overwrite it. Secure cookies (and __Host-) are
// accepted over http://localhost, so this works in dev too.
const CookieName = "__Host-vg_session"

// sessionAAD domain-separates cookie seals from any other future use of
// the same key: GCM authenticates this label, so a blob sealed for a
// different purpose cannot be replayed into the session cookie. Changing
// this label invalidates every live cookie.
const sessionAAD = "vg_session/v1"

// Codec seals Sessions into cookie values with AES-256-GCM. The sealed
// form is base64url(nonce || ciphertext).
type Codec struct {
	aead   cipher.AEAD
	secure bool
}

// NewCodec builds a Codec from a base64 (std) encoded 32-byte key.
func NewCodec(keyB64 string, secure bool) (*Codec, error) {
	key, err := base64.StdEncoding.DecodeString(keyB64)
	if err != nil {
		return nil, fmt.Errorf("session: decode cookie key: %w", err)
	}
	if len(key) != 32 {
		return nil, errors.New("session: cookie key must be 32 bytes")
	}
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, fmt.Errorf("session: cipher: %w", err)
	}
	aead, err := cipher.NewGCM(block)
	if err != nil {
		return nil, fmt.Errorf("session: gcm: %w", err)
	}
	return &Codec{aead: aead, secure: secure}, nil
}

// Seal encrypts a session into a cookie value.
func (c *Codec) Seal(s Session) (string, error) {
	plain, err := json.Marshal(s) //nolint:gosec // G117: marshaling the token pair is intentional; the output is AES-GCM sealed before use
	if err != nil {
		return "", fmt.Errorf("session: marshal: %w", err)
	}
	nonce := make([]byte, c.aead.NonceSize())
	if _, err := rand.Read(nonce); err != nil {
		return "", fmt.Errorf("session: nonce: %w", err)
	}
	sealed := c.aead.Seal(nonce, nonce, plain, []byte(sessionAAD))
	return base64.RawURLEncoding.EncodeToString(sealed), nil
}

// Open decrypts a cookie value. Any error means "no session": expired
// keys, tampering, and garbage all look the same to the caller.
func (c *Codec) Open(value string) (Session, error) {
	raw, err := base64.RawURLEncoding.Strict().DecodeString(value)
	if err != nil {
		return Session{}, fmt.Errorf("session: decode: %w", err)
	}
	if len(raw) < c.aead.NonceSize() {
		return Session{}, errors.New("session: sealed value too short")
	}
	nonce, ct := raw[:c.aead.NonceSize()], raw[c.aead.NonceSize():]
	plain, err := c.aead.Open(nil, nonce, ct, []byte(sessionAAD))
	if err != nil {
		return Session{}, fmt.Errorf("session: open: %w", err)
	}
	var s Session
	if err := json.Unmarshal(plain, &s); err != nil {
		return Session{}, fmt.Errorf("session: unmarshal: %w", err)
	}
	if s.AccessToken == "" || s.RefreshToken == "" {
		return Session{}, errors.New("session: sealed payload missing tokens")
	}
	return s, nil
}

// Cookie wraps a sealed value in the session cookie. maxAge is seconds
// (the refresh token's remaining life, so cookie and session expire
// together).
func (c *Codec) Cookie(sealed string, maxAge int) *http.Cookie {
	return &http.Cookie{ //nolint:gosec // G124: Secure follows the codec setting (false only in dev)
		Name:     CookieName,
		Value:    sealed,
		Path:     "/",
		MaxAge:   maxAge,
		HttpOnly: true,
		Secure:   c.secure,
		SameSite: http.SameSiteLaxMode,
	}
}

// ClearCookie expires the session cookie immediately.
func (c *Codec) ClearCookie() *http.Cookie {
	return &http.Cookie{ //nolint:gosec // G124: Secure follows the codec setting (false only in dev)
		Name:     CookieName,
		Value:    "",
		Path:     "/",
		MaxAge:   -1,
		HttpOnly: true,
		Secure:   c.secure,
		SameSite: http.SameSiteLaxMode,
	}
}
