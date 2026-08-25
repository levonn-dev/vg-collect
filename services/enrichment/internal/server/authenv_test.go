// White-box test package (like the bff's server tests): handler tests
// reach the unexported now seam for staleness math.
package server

import (
	"testing"

	"github.com/levonn-dev/vgkeep/libs/go/jwtauth"
	"github.com/levonn-dev/vgkeep/libs/go/jwtauthtest"
)

// authEnv is an in-process JWKS + signer: handler tests exercise the
// real validator instead of stubbing authentication.
type authEnv struct {
	env *jwtauthtest.Env
}

func newAuthEnv(t *testing.T) *authEnv {
	t.Helper()
	return &authEnv{env: jwtauthtest.NewEnv(t)}
}

// token mints a valid access JWT for sub with the given roles.
func (a *authEnv) token(t *testing.T, sub string, roles []string) string {
	t.Helper()
	return a.env.Token(t, sub, roles...)
}

func (a *authEnv) validator() *jwtauth.Validator {
	return a.env.Validator
}

// serviceToken mints a valid access JWT carrying token_use=service (no
// roles) for sub, a machine credential like the catalog-refresh CronJob's.
func (a *authEnv) serviceToken(t *testing.T, sub string) string {
	t.Helper()
	return a.env.ServiceToken(t, sub)
}
