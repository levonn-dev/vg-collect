package config_test

import (
	"strings"
	"testing"

	"github.com/levonn-dev/vgkeep/libs/go/config"
)

// Synthetic variable names keep these tests hermetic; real names like DATABASE_URL leak in
// from CI/shell environments and turn the missing-required test falsely green.
type testCfg struct {
	Addr  string `env:"TEST_VG_HTTP_ADDR" envDefault:":8080"`
	DBURL string `env:"TEST_VG_DB_URL,required"`
}

func TestLoad_DefaultsAndRequired(t *testing.T) {
	t.Setenv("TEST_VG_DB_URL", "postgres://x")
	got, err := config.Load[testCfg]()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if got.Addr != ":8080" || got.DBURL != "postgres://x" {
		t.Fatalf("got %+v", got)
	}
}

func TestLoad_EnvOverridesDefault(t *testing.T) {
	t.Setenv("TEST_VG_DB_URL", "postgres://x")
	t.Setenv("TEST_VG_HTTP_ADDR", ":9999")
	got, err := config.Load[testCfg]()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if got.Addr != ":9999" {
		t.Fatalf("override not applied: %+v", got)
	}
}

func TestLoad_MissingRequired(t *testing.T) {
	_, err := config.Load[testCfg]()
	if err == nil {
		t.Fatal("want error for missing TEST_VG_DB_URL")
	}
	const wantPrefix = "config: "
	if !strings.HasPrefix(err.Error(), wantPrefix) {
		t.Fatalf("error %q missing %q prefix", err.Error(), wantPrefix)
	}
}
