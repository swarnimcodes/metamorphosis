// Package testutil gives tests a fresh, fully migrated database each, like sqlx::test did.
package testutil

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"net/url"
	"os"
	"testing"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/smarniw/metamorphosis/internal/migrate"
	"github.com/smarniw/metamorphosis/migrations"
)

var (
	adminURL string
	template string
)

func randomName(prefix string) string {
	b := make([]byte, 6)
	_, _ = rand.Read(b)
	return prefix + hex.EncodeToString(b)
}

// withDB returns the admin URL pointed at another database.
func withDB(raw, db string) string {
	u, err := url.Parse(raw)
	if err != nil {
		panic(err)
	}
	u.Path = "/" + db
	return u.String()
}

func admin(ctx context.Context) (*pgx.Conn, error) { return pgx.Connect(ctx, adminURL) }

// Main is for TestMain: it builds one migrated template database, runs the tests, then drops it.
// TEST_DATABASE_URL points at any database on a Postgres 18 server whose role may create databases.
func Main(m *testing.M) int {
	adminURL = os.Getenv("TEST_DATABASE_URL")
	if adminURL == "" {
		adminURL = "postgres://postgres@localhost/postgres"
	}
	ctx := context.Background()
	conn, err := admin(ctx)
	if err != nil {
		fmt.Fprintln(os.Stderr, "testutil: cannot reach Postgres:", err)
		return 1
	}
	defer conn.Close(ctx)

	template = randomName("metamorph_tmpl_")
	if _, err := conn.Exec(ctx, "create database "+template); err != nil {
		fmt.Fprintln(os.Stderr, "testutil: creating template:", err)
		return 1
	}
	defer conn.Exec(ctx, "drop database if exists "+template+" with (force)") //nolint:errcheck

	pool, err := pgxpool.New(ctx, withDB(adminURL, template))
	if err == nil {
		err = migrate.Run(ctx, pool, migrations.FS)
		pool.Close()
	}
	if err != nil {
		fmt.Fprintln(os.Stderr, "testutil: migrating template:", err)
		return 1
	}
	return m.Run()
}

// NewDB returns a pool on a private copy of the migrated schema, dropped when the test ends.
func NewDB(t testing.TB) *pgxpool.Pool {
	t.Helper()
	ctx := context.Background()
	conn, err := admin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	name := randomName("metamorph_test_")
	if _, err := conn.Exec(ctx, fmt.Sprintf("create database %s template %s", name, template)); err != nil {
		conn.Close(ctx)
		t.Fatal(err)
	}
	pool, err := pgxpool.New(ctx, withDB(adminURL, name))
	if err != nil {
		conn.Close(ctx)
		t.Fatal(err)
	}
	t.Cleanup(func() {
		pool.Close()
		_, _ = conn.Exec(ctx, "drop database if exists "+name+" with (force)")
		conn.Close(ctx)
	})
	return pool
}
