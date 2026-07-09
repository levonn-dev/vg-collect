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

func TestUpsert_FillsOnCreateOnly(t *testing.T) {
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

	// A later login must not clobber the profile: same id, same fields.
	avatar := "https://img.example/a.png"
	u2, err := s.Upsert(ctx, "a@example.com", "Alice II", &avatar)
	if err != nil {
		t.Fatalf("Upsert existing: %v", err)
	}
	if u2.ID != u1.ID {
		t.Fatalf("upsert created a duplicate: %s vs %s", u1.ID, u2.ID)
	}
	if u2.DisplayName != "Alice" || u2.AvatarURL != nil {
		t.Fatalf("login overwrote the profile: %+v", u2)
	}
	if !u2.UpdatedAt.Equal(u1.UpdatedAt) {
		t.Fatalf("login touched updated_at: %v vs %v", u2.UpdatedAt, u1.UpdatedAt)
	}

	// citext: case-insensitive email still resolves the same account.
	u3, err := s.Upsert(ctx, "A@EXAMPLE.COM", "Alice III", nil)
	if err != nil {
		t.Fatalf("Upsert citext: %v", err)
	}
	if u3.ID != u1.ID {
		t.Fatalf("citext email uniqueness failed: %s vs %s", u3.ID, u1.ID)
	}
}

func TestUpdate_FieldSemantics(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	created, err := s.Upsert(ctx, "c@example.com", "Carol", nil)
	if err != nil {
		t.Fatal(err)
	}

	name := "Carol Prime"
	avatar := "https://img.example/c.png"
	u, err := s.Update(ctx, created.ID, &name, &avatar)
	if err != nil {
		t.Fatalf("Update: %v", err)
	}
	if u.DisplayName != "Carol Prime" || u.AvatarURL == nil || *u.AvatarURL != avatar {
		t.Fatalf("update not applied: %+v", u)
	}
	if !u.UpdatedAt.After(created.UpdatedAt) {
		t.Fatalf("updated_at not bumped")
	}
	if len(u.Roles) != 1 || u.Roles[0] != "user" {
		t.Fatalf("roles lost: %v", u.Roles)
	}

	// nil keeps, empty string clears the avatar.
	empty := ""
	u, err = s.Update(ctx, created.ID, nil, &empty)
	if err != nil {
		t.Fatalf("Update clear: %v", err)
	}
	if u.DisplayName != "Carol Prime" || u.AvatarURL != nil {
		t.Fatalf("clear semantics wrong: %+v", u)
	}

	_, err = s.Update(ctx, uuid.New(), &name, nil)
	if !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("want ErrNotFound, got %v", err)
	}
}

func TestDelete_IdempotentAndCascades(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	created, err := s.Upsert(ctx, "d@example.com", "Dave", nil)
	if err != nil {
		t.Fatal(err)
	}
	if err := s.Delete(ctx, created.ID); err != nil {
		t.Fatalf("Delete: %v", err)
	}
	if _, err := s.Get(ctx, created.ID); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("user survived: %v", err)
	}
	// user_roles cascaded: re-creating by email gets a fresh id + role.
	again, err := s.Upsert(ctx, "d@example.com", "Dave", nil)
	if err != nil || again.ID == created.ID || len(again.Roles) != 1 {
		t.Fatalf("recreate = %+v %v", again, err)
	}
	if err := s.Delete(ctx, created.ID); err != nil {
		t.Fatalf("second delete: %v", err)
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
