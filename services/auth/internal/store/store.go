// Package store owns the auth service's SQL: identities, signing keys, OAuth states, and
// refresh-token sessions. No other package writes queries against this schema.
package store

import (
	"context"
	"crypto/ed25519"
	"encoding/base64"
	"errors"
	"fmt"
	"slices"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/levonn-dev/vgkeep/libs/go/pgkit"
)

var (
	ErrStateNotFound    = errors.New("auth state not found or expired")
	ErrRefreshNotFound  = errors.New("refresh token not found")
	ErrRefreshExpired   = errors.New("refresh token expired")
	ErrRefreshRevoked   = errors.New("refresh token family already revoked")
	ErrIdentityNotFound = errors.New("identity not found")
	ErrIdentityTaken    = errors.New("identity bound to another user")
	ErrLastIdentity     = errors.New("cannot remove the last identity")
)

// ReuseError reports a consumed or revoked refresh token presented again; the whole family
// is revoked, and RevokedJTIs are the family's access-token jtis that may still be alive.
type ReuseError struct {
	RevokedJTIs []string
}

func (e *ReuseError) Error() string { return "store: refresh token reuse detected" }

type Store struct{ pool *pgxpool.Pool }

func New(pool *pgxpool.Pool) *Store { return &Store{pool: pool} }

// RegisterSigningKey records a verification key for the JWKS; kids derive deterministically from the key, so re-registration on boot is a no-op.
func (s *Store) RegisterSigningKey(ctx context.Context, kid string, pub ed25519.PublicKey) error {
	_, err := s.pool.Exec(ctx, `
		INSERT INTO signing_keys (kid, public_key) VALUES ($1, $2)
		ON CONFLICT (kid) DO NOTHING`,
		kid, base64.RawURLEncoding.EncodeToString(pub))
	if err != nil {
		return fmt.Errorf("store: register signing key: %w", err)
	}
	return nil
}

type SigningKey struct {
	Kid          string
	PublicKeyB64 string // base64url raw key, served verbatim as the JWKS x field
}

// ActiveSigningKeys returns every non-retired key, oldest first; a rotated-out key stays until an operator retires it.
func (s *Store) ActiveSigningKeys(ctx context.Context) ([]SigningKey, error) {
	rows, err := s.pool.Query(ctx, `
		SELECT kid, public_key FROM signing_keys
		WHERE retired_at IS NULL ORDER BY created_at`)
	if err != nil {
		return nil, fmt.Errorf("store: signing keys: %w", err)
	}
	return pgkit.ScanAll(rows, nil, func(r pgx.Rows) (SigningKey, error) {
		var k SigningKey
		if err := r.Scan(&k.Kid, &k.PublicKeyB64); err != nil {
			return SigningKey{}, fmt.Errorf("store: scan signing key: %w", err)
		}
		return k, nil
	})
}

type AuthState struct {
	State        string
	PKCEVerifier string
	Nonce        string
	Provider     string
	ExpiresAt    time.Time
	// LinkUserID marks the flow as an account-link for that user; nil is a normal login.
	LinkUserID *uuid.UUID
}

// CreateState stores a pending OAuth round trip and opportunistically sweeps expired rows (the table self-cleans, no background job).
func (s *Store) CreateState(ctx context.Context, st AuthState) error {
	if _, err := s.pool.Exec(ctx,
		`DELETE FROM auth_states WHERE expires_at < now()`); err != nil {
		return fmt.Errorf("store: sweep states: %w", err)
	}
	_, err := s.pool.Exec(ctx, `
		INSERT INTO auth_states (state, pkce_verifier, nonce, provider, expires_at, link_user_id)
		VALUES ($1, $2, $3, $4, $5, $6)`,
		st.State, st.PKCEVerifier, st.Nonce, st.Provider, st.ExpiresAt, st.LinkUserID)
	if err != nil {
		return fmt.Errorf("store: create state: %w", err)
	}
	return nil
}

// ConsumeState atomically deletes and returns a pending state; consumable exactly once and
// only before expiry. Everything else is ErrStateNotFound (no oracle for which condition failed).
func (s *Store) ConsumeState(ctx context.Context, state string) (AuthState, error) {
	st := AuthState{State: state}
	err := s.pool.QueryRow(ctx, `
		DELETE FROM auth_states
		WHERE state = $1 AND expires_at > now()
		RETURNING pkce_verifier, nonce, provider, expires_at, link_user_id`, state,
	).Scan(&st.PKCEVerifier, &st.Nonce, &st.Provider, &st.ExpiresAt, &st.LinkUserID)
	if errors.Is(err, pgx.ErrNoRows) {
		return AuthState{}, ErrStateNotFound
	}
	if err != nil {
		return AuthState{}, fmt.Errorf("store: consume state: %w", err)
	}
	return st, nil
}

type Identity struct {
	ID        uuid.UUID
	Provider  string
	Subject   string
	Email     *string
	UserID    uuid.UUID
	CreatedAt time.Time
}

// ResolveIdentity answers "whose login is this" for (provider, subject), refreshing the
// stored email in the same statement. ErrIdentityNotFound means a first-time identity.
func (s *Store) ResolveIdentity(ctx context.Context, provider, subject, email string) (Identity, error) {
	id := Identity{Provider: provider, Subject: subject}
	err := s.pool.QueryRow(ctx, `
		UPDATE identities SET email = $3
		WHERE provider = $1 AND provider_subject = $2
		RETURNING id, user_id, email, created_at`,
		provider, subject, email,
	).Scan(&id.ID, &id.UserID, &id.Email, &id.CreatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return Identity{}, ErrIdentityNotFound
	}
	if err != nil {
		return Identity{}, fmt.Errorf("store: resolve identity: %w", err)
	}
	return id, nil
}

// BindIdentity maps (provider, subject) to a user, insert-only: an identity never silently
// moves between accounts. The same user again is an idempotent email refresh; another user's answers ErrIdentityTaken.
func (s *Store) BindIdentity(ctx context.Context, provider, subject, email string, userID uuid.UUID) error {
	err := pgx.BeginFunc(ctx, s.pool, func(tx pgx.Tx) error {
		tag, err := tx.Exec(ctx, `
			INSERT INTO identities (provider, provider_subject, email, user_id)
			VALUES ($1, $2, $3, $4)
			ON CONFLICT (provider, provider_subject) DO NOTHING`,
			provider, subject, email, userID)
		if err != nil {
			return fmt.Errorf("store: bind identity: %w", err)
		}
		if tag.RowsAffected() == 1 {
			return nil
		}
		var owner uuid.UUID
		if err := tx.QueryRow(ctx,
			`SELECT user_id FROM identities WHERE provider = $1 AND provider_subject = $2`,
			provider, subject).Scan(&owner); err != nil {
			return fmt.Errorf("store: bind identity owner: %w", err)
		}
		if owner != userID {
			return ErrIdentityTaken
		}
		if _, err := tx.Exec(ctx,
			`UPDATE identities SET email = $3 WHERE provider = $1 AND provider_subject = $2`,
			provider, subject, email); err != nil {
			return fmt.Errorf("store: bind identity email: %w", err)
		}
		return nil
	})
	return err
}

// RebindIdentity moves an identity to a new user unconditionally, inserting when absent.
// Reserved for the heal path, where the caller has verified the previous owner is gone.
func (s *Store) RebindIdentity(ctx context.Context, provider, subject, email string, userID uuid.UUID) error {
	_, err := s.pool.Exec(ctx, `
		INSERT INTO identities (provider, provider_subject, email, user_id)
		VALUES ($1, $2, $3, $4)
		ON CONFLICT (provider, provider_subject)
		DO UPDATE SET user_id = EXCLUDED.user_id, email = EXCLUDED.email`,
		provider, subject, email, userID)
	if err != nil {
		return fmt.Errorf("store: rebind identity: %w", err)
	}
	return nil
}

// ListIdentities returns a user's linked logins, oldest first.
func (s *Store) ListIdentities(ctx context.Context, userID uuid.UUID) ([]Identity, error) {
	rows, err := s.pool.Query(ctx, `
		SELECT id, provider, provider_subject, email, user_id, created_at
		FROM identities WHERE user_id = $1 ORDER BY created_at, provider`, userID)
	if err != nil {
		return nil, fmt.Errorf("store: list identities: %w", err)
	}
	return pgkit.ScanAll(rows, nil, func(r pgx.Rows) (Identity, error) {
		var id Identity
		if err := r.Scan(&id.ID, &id.Provider, &id.Subject, &id.Email, &id.UserID, &id.CreatedAt); err != nil {
			return Identity{}, fmt.Errorf("store: scan identity: %w", err)
		}
		return id, nil
	})
}

// DeleteIdentity unlinks one login. The row lock on the user's identities serializes
// concurrent unlinks so the last-identity guard can't be raced; unknown ids answer ErrIdentityNotFound.
func (s *Store) DeleteIdentity(ctx context.Context, userID, identityID uuid.UUID) error {
	return pgx.BeginFunc(ctx, s.pool, func(tx pgx.Tx) error {
		rows, err := tx.Query(ctx,
			`SELECT id FROM identities WHERE user_id = $1 FOR UPDATE`, userID)
		if err != nil {
			return fmt.Errorf("store: lock identities: %w", err)
		}
		defer rows.Close()
		var ids []uuid.UUID
		for rows.Next() {
			var id uuid.UUID
			if err := rows.Scan(&id); err != nil {
				return fmt.Errorf("store: scan identity id: %w", err)
			}
			ids = append(ids, id)
		}
		if err := rows.Err(); err != nil {
			return err
		}
		found := slices.Contains(ids, identityID)
		if !found {
			return ErrIdentityNotFound
		}
		if len(ids) == 1 {
			return ErrLastIdentity
		}
		if _, err := tx.Exec(ctx,
			`DELETE FROM identities WHERE id = $1 AND user_id = $2`, identityID, userID); err != nil {
			return fmt.Errorf("store: delete identity: %w", err)
		}
		return nil
	})
}

// DeleteUserAuth erases a user's auth footprint for account deletion: every identity plus
// revoking every live refresh family, in one transaction. Idempotent.
func (s *Store) DeleteUserAuth(ctx context.Context, userID uuid.UUID) error {
	return pgx.BeginFunc(ctx, s.pool, func(tx pgx.Tx) error {
		if _, err := tx.Exec(ctx,
			`DELETE FROM identities WHERE user_id = $1`, userID); err != nil {
			return fmt.Errorf("store: delete identities: %w", err)
		}
		if _, err := tx.Exec(ctx, `
			UPDATE refresh_tokens SET revoked_at = now()
			WHERE user_id = $1 AND revoked_at IS NULL`, userID); err != nil {
			return fmt.Errorf("store: revoke user families: %w", err)
		}
		return nil
	})
}

// CreateSession starts a refresh family; accessJTI is the jti of the access token minted
// alongside it. It sweeps dead rows (used/revoked) past a 30-day retention before inserting,
// keeping reuse forensics available while bounding table growth (no background job needed).
// migration 000004 nulls a swept row's parent_hash link on delete, so the sweep never blocks.
func (s *Store) CreateSession(ctx context.Context, tokenHash string, userID uuid.UUID, accessJTI string, expiresAt time.Time) error {
	if _, err := s.pool.Exec(ctx, `
		DELETE FROM refresh_tokens
		WHERE (used_at IS NOT NULL OR revoked_at IS NOT NULL)
		AND expires_at < now() - interval '30 days'`); err != nil {
		return fmt.Errorf("store: sweep refresh tokens: %w", err)
	}
	_, err := s.pool.Exec(ctx, `
		INSERT INTO refresh_tokens (token_hash, family_id, user_id, last_access_jti, expires_at)
		VALUES ($1, gen_random_uuid(), $2, $3, $4)`,
		tokenHash, userID, accessJTI, expiresAt)
	if err != nil {
		return fmt.Errorf("store: create session: %w", err)
	}
	return nil
}

type Session struct {
	UserID uuid.UUID
}

// PeekSession resolves a token to its user without consuming it; callers fetch roles BEFORE
// Rotate so an upstream failure can't strand the client with a consumed token and no replacement.
// ErrRefreshRevoked (explicit logout/reuse revocation) lets the handler short-circuit to
// refresh_reused; a used_at-only row still goes through Rotate so the full reuse path collects live JTIs.
func (s *Store) PeekSession(ctx context.Context, tokenHash string) (Session, error) {
	var sess Session
	var revokedAt *time.Time
	err := s.pool.QueryRow(ctx,
		`SELECT user_id, revoked_at FROM refresh_tokens WHERE token_hash = $1`, tokenHash,
	).Scan(&sess.UserID, &revokedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return Session{}, ErrRefreshNotFound
	}
	if err != nil {
		return Session{}, fmt.Errorf("store: peek session: %w", err)
	}
	if revokedAt != nil {
		return Session{}, ErrRefreshRevoked
	}
	return sess, nil
}

type RotateResult struct {
	UserID    uuid.UUID
	ExpiresAt time.Time // absolute family expiry, inherited by the new token
}

// Rotate consumes the presented token and issues its child in one transaction; the row lock
// serializes concurrent refreshes, and the loser (used_at set) takes the reuse path.
// A consumed/revoked token means reuse: the family is revoked, and ReuseError carries
// last_access_jti for rows created within jtiWindow. This revocation commits even on error return; only a pre-commit abort (e.g. cancellation) rolls back.
func (s *Store) Rotate(ctx context.Context, presentedHash, newHash, newAccessJTI string, jtiWindow time.Duration) (RotateResult, error) {
	var res RotateResult
	var reuse *ReuseError
	err := pgx.BeginFunc(ctx, s.pool, func(tx pgx.Tx) error {
		var familyID uuid.UUID
		var expiresAt time.Time
		var usedAt, revokedAt *time.Time
		err := tx.QueryRow(ctx, `
			SELECT family_id, user_id, expires_at, used_at, revoked_at
			FROM refresh_tokens WHERE token_hash = $1 FOR UPDATE`, presentedHash,
		).Scan(&familyID, &res.UserID, &expiresAt, &usedAt, &revokedAt)
		if errors.Is(err, pgx.ErrNoRows) {
			return ErrRefreshNotFound
		}
		if err != nil {
			return fmt.Errorf("store: lock refresh token: %w", err)
		}

		if usedAt != nil || revokedAt != nil {
			// Reuse or use-after-logout. Lock every family row first: a concurrent legitimate
			// rotation holds its parent's row lock while inserting a child, so this lock serializes
			// against it and the UPDATE below sees any child committed while we waited. Without it, a
			// mid-flight rotation could fork a live child out of a family the system believes fully revoked.
			if _, err := tx.Exec(ctx, `
				SELECT token_hash FROM refresh_tokens
				WHERE family_id = $1 FOR UPDATE`, familyID); err != nil {
				return fmt.Errorf("store: lock family: %w", err)
			}
			// Revoke and collect in one statement: revocation can never commit partially relative to the reported jtis.
			rows, err := tx.Query(ctx, `
				UPDATE refresh_tokens SET revoked_at = now()
				WHERE family_id = $1 AND revoked_at IS NULL
				RETURNING last_access_jti, created_at`, familyID)
			if err != nil {
				return fmt.Errorf("store: revoke family: %w", err)
			}
			defer rows.Close()
			cutoff := time.Now().Add(-jtiWindow)
			reuse = &ReuseError{}
			for rows.Next() {
				var jti string
				var createdAt time.Time
				if err := rows.Scan(&jti, &createdAt); err != nil {
					return fmt.Errorf("store: scan jti: %w", err)
				}
				if createdAt.After(cutoff) {
					reuse.RevokedJTIs = append(reuse.RevokedJTIs, jti)
				}
			}
			return rows.Err()
		}

		if !expiresAt.After(time.Now()) {
			return ErrRefreshExpired
		}

		res.ExpiresAt = expiresAt
		if _, err := tx.Exec(ctx,
			`UPDATE refresh_tokens SET used_at = now() WHERE token_hash = $1`, presentedHash); err != nil {
			return fmt.Errorf("store: consume refresh token: %w", err)
		}
		// The child inherits the family's ABSOLUTE expiry: a session hard stops 30 days after login regardless of refresh activity.
		if _, err := tx.Exec(ctx, `
			INSERT INTO refresh_tokens
				(token_hash, parent_hash, family_id, user_id, last_access_jti, expires_at)
			VALUES ($1, $2, $3, $4, $5, $6)`,
			newHash, presentedHash, familyID, res.UserID, newAccessJTI, expiresAt); err != nil {
			return fmt.Errorf("store: insert rotated token: %w", err)
		}
		return nil
	})
	if err != nil {
		return RotateResult{}, err
	}
	if reuse != nil {
		return RotateResult{}, reuse
	}
	return res, nil
}

// RevokeFamilyByToken revokes the token's whole family (logout); an unknown token is a
// no-op, so logout is idempotent. The family-wide lock serializes against an in-flight rotation.
func (s *Store) RevokeFamilyByToken(ctx context.Context, tokenHash string) error {
	return pgx.BeginFunc(ctx, s.pool, func(tx pgx.Tx) error {
		var familyID uuid.UUID
		err := tx.QueryRow(ctx,
			`SELECT family_id FROM refresh_tokens WHERE token_hash = $1`, tokenHash,
		).Scan(&familyID)
		if errors.Is(err, pgx.ErrNoRows) {
			return nil
		}
		if err != nil {
			return fmt.Errorf("store: find family: %w", err)
		}
		if _, err := tx.Exec(ctx, `
			SELECT token_hash FROM refresh_tokens
			WHERE family_id = $1 FOR UPDATE`, familyID); err != nil {
			return fmt.Errorf("store: lock family: %w", err)
		}
		if _, err := tx.Exec(ctx, `
			UPDATE refresh_tokens SET revoked_at = now()
			WHERE family_id = $1 AND revoked_at IS NULL`, familyID); err != nil {
			return fmt.Errorf("store: revoke family: %w", err)
		}
		return nil
	})
}
