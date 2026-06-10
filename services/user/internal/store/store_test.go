package store_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/testcontainers/testcontainers-go"
	tcpostgres "github.com/testcontainers/testcontainers-go/modules/postgres"
	"github.com/testcontainers/testcontainers-go/wait"

	"github.com/levonn-dev/vg-collect/libs/go/pgkit"
	"github.com/levonn-dev/vg-collect/services/user/internal/store"
	"github.com/levonn-dev/vg-collect/services/user/migrations"
)

func newTestStore(t *testing.T) *store.Store {
	t.Helper()
	if testing.Short() {
		t.Skip("requires docker")
	}
	ctx := context.Background()
	pg, err := tcpostgres.Run(ctx, "postgres:17-alpine",
		tcpostgres.WithDatabase("user"), tcpostgres.WithUsername("u"), tcpostgres.WithPassword("p"),
		testcontainers.WithWaitStrategy(
			wait.ForLog("database system is ready to accept connections").
				WithOccurrence(2).WithStartupTimeout(60*time.Second)))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = pg.Terminate(ctx) })
	url, err := pg.ConnectionString(ctx, "sslmode=disable")
	if err != nil {
		t.Fatal(err)
	}
	if err := pgkit.Migrate(url, migrations.FS, "."); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	pool, err := pgkit.Connect(ctx, url)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(pool.Close)
	return store.New(pool)
}

func TestUpsert_CreateThenUpdate(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()

	u1, err := s.Upsert(ctx, "a@example.com", "Alice", nil)
	if err != nil {
		t.Fatalf("Upsert: %v", err)
	}
	if u1.Email != "a@example.com" || u1.DisplayName != "Alice" {
		t.Fatalf("created = %+v", u1)
	}
	if len(u1.Roles) != 1 || u1.Roles[0] != "user" {
		t.Fatalf("default role missing: %v", u1.Roles)
	}

	avatar := "https://img.example/a.png"
	u2, err := s.Upsert(ctx, "a@example.com", "Alice II", &avatar)
	if err != nil {
		t.Fatalf("Upsert update: %v", err)
	}
	if u2.ID != u1.ID {
		t.Fatalf("upsert created a duplicate: %s vs %s", u1.ID, u2.ID)
	}
	if u2.DisplayName != "Alice II" || u2.AvatarURL == nil || *u2.AvatarURL != avatar {
		t.Fatalf("update not applied: %+v", u2)
	}

	u3, err := s.Upsert(ctx, "A@EXAMPLE.COM", "Alice III", nil)
	if err != nil {
		t.Fatalf("Upsert citext: %v", err)
	}
	if u3.ID != u1.ID {
		t.Fatalf("citext email uniqueness failed: %s vs %s", u3.ID, u1.ID)
	}
}

func TestGet(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()

	created, err := s.Upsert(ctx, "b@example.com", "Bob", nil)
	if err != nil {
		t.Fatal(err)
	}
	got, err := s.Get(ctx, created.ID)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if got.Email != "b@example.com" || len(got.Roles) != 1 {
		t.Fatalf("got = %+v", got)
	}

	_, err = s.Get(ctx, uuid.New())
	if !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("want ErrNotFound, got %v", err)
	}
}
