package config_test

import (
	"testing"

	"github.com/levonn-dev/vgkeep/libs/go/config"
)

func TestRequireCAForRediss(t *testing.T) {
	cases := []struct {
		name    string
		url     string
		caFile  string
		wantErr bool
	}{
		{"rediss without CA fails", "rediss://valkey:6379/0", "", true},
		{"rediss with CA passes", "rediss://valkey:6379/0", "/etc/ssl/valkey-ca.crt", false},
		{"plain redis passes", "redis://valkey:6379/0", "", false},
		{"empty URL passes", "", "", false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := config.RequireCAForRediss(tc.url, tc.caFile)
			if tc.wantErr && err == nil {
				t.Fatal("want error, got nil")
			}
			if !tc.wantErr && err != nil {
				t.Fatalf("want no error, got %v", err)
			}
		})
	}
}

// TestRequireCAForRediss_ErrorMessage pins the exact string every call site depends on: all
// are byte-identical, so there is one message to preserve, not one per caller.
func TestRequireCAForRediss_ErrorMessage(t *testing.T) {
	err := config.RequireCAForRediss("rediss://valkey:6379/0", "")
	const want = "config: VALKEY_CA_FILE is required for a rediss:// VALKEY_URL"
	if err == nil || err.Error() != want {
		t.Fatalf("err = %v, want %q", err, want)
	}
}
