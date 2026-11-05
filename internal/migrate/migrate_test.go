package migrate_test

import (
	"context"
	"io/fs"
	"os"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"testing"
	"testing/fstest"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/smarniw/metamorphosis/internal/migrate"
	"github.com/smarniw/metamorphosis/internal/testutil"
	"github.com/smarniw/metamorphosis/migrations"
)

func TestMain(m *testing.M) { os.Exit(testutil.Main(m)) }

var nameRe = regexp.MustCompile(`^(\d+)_(.+)\.sql$`)

// files returns the embedded migrations (only the .sql files), so tests can add to them.
func files(t *testing.T) fstest.MapFS {
	t.Helper()
	out := fstest.MapFS{}
	names, err := fs.Glob(migrations.FS, "*.sql")
	if err != nil || len(names) == 0 {
		t.Fatalf("no migrations: %v", err)
	}
	for _, n := range names {
		b, err := fs.ReadFile(migrations.FS, n)
		if err != nil {
			t.Fatal(err)
		}
		out[n] = &fstest.MapFile{Data: b}
	}
	return out
}

func count(t *testing.T, pool *pgxpool.Pool, query string, args ...any) int {
	t.Helper()
	var n int
	if err := pool.QueryRow(context.Background(), query, args...).Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n
}

func exists(t *testing.T, pool *pgxpool.Pool, table string) bool {
	t.Helper()
	var ok bool
	if err := pool.QueryRow(context.Background(), "select to_regclass($1) is not null", table).Scan(&ok); err != nil {
		t.Fatal(err)
	}
	return ok
}

// asSqlxMigrated turns a goose-migrated database into what the previous migrator left behind: the same schema
// and a _sqlx_migrations table, and no goose table.
func asSqlxMigrated(t *testing.T, pool *pgxpool.Pool, fsys fs.FS) {
	t.Helper()
	ctx := context.Background()
	for _, stmt := range []string{
		`drop table goose_db_version`,
		`create table _sqlx_migrations (version bigint primary key, description text not null,
		   installed_on timestamptz not null default now(), success boolean not null, checksum bytea not null,
		   execution_time bigint not null)`,
	} {
		if _, err := pool.Exec(ctx, stmt); err != nil {
			t.Fatal(err)
		}
	}
	names, _ := fs.Glob(fsys, "*.sql")
	for _, n := range names {
		m := nameRe.FindStringSubmatch(n)
		v, _ := strconv.ParseInt(m[1], 10, 64)
		if _, err := pool.Exec(ctx, `insert into _sqlx_migrations (version, description, success, checksum, execution_time)
		   values ($1, $2, true, '\x00', 1)`, v, strings.ReplaceAll(m[2], "_", " ")); err != nil {
			t.Fatal(err)
		}
	}
}

func TestAFreshDatabaseMigratesAndRunningAgainChangesNothing(t *testing.T) {
	pool := testutil.NewEmptyDB(t)
	fsys := files(t)
	if err := migrate.Run(context.Background(), pool, fsys); err != nil {
		t.Fatal(err)
	}
	want := len(fsys) + 1 // goose records version 0 too
	if got := count(t, pool, "select count(*) from goose_db_version"); got != want {
		t.Errorf("goose_db_version has %d rows, want %d", got, want)
	}
	if !exists(t, pool, "users") || !exists(t, pool, "workouts") {
		t.Error("the schema was not created")
	}
	if exists(t, pool, "_sqlx_migrations") {
		t.Error("a fresh database must not get the old migrator's table")
	}
	if err := migrate.Run(context.Background(), pool, fsys); err != nil {
		t.Fatal(err)
	}
	if got := count(t, pool, "select count(*) from goose_db_version"); got != want {
		t.Errorf("a second run changed goose_db_version to %d rows", got)
	}
}

func TestADatabaseTheOldMigratorMigratedIsAdoptedWithoutRunningAnything(t *testing.T) {
	pool := testutil.NewDB(t) // schema from the migrations, standing in for production's
	fsys := files(t)
	asSqlxMigrated(t, pool, fsys)
	ctx := context.Background()
	// Data that a re-run of any migration would destroy or duplicate.
	if _, err := pool.Exec(ctx, `insert into users (email, username, display_name) values ('a@example.com','adopted','A')`); err != nil {
		t.Fatal(err)
	}
	exercises := count(t, pool, "select count(*) from exercises")

	if err := migrate.Run(ctx, pool, fsys); err != nil {
		t.Fatal(err)
	}
	if got, want := count(t, pool, "select count(*) from goose_db_version"), len(fsys)+1; got != want {
		t.Errorf("goose_db_version has %d rows, want %d", got, want)
	}
	if got := count(t, pool, "select count(*) from users where username = 'adopted'"); got != 1 {
		t.Errorf("the user row is gone or duplicated: %d", got)
	}
	if got := count(t, pool, "select count(*) from exercises"); got != exercises {
		t.Errorf("the seed data changed: %d exercises, was %d", got, exercises)
	}
	if got := count(t, pool, "select count(*) from _sqlx_migrations"); got != len(fsys) {
		t.Errorf("the old table must be left alone, has %d rows", got)
	}
	// Running again, as every later start does, is a no-op.
	if err := migrate.Run(ctx, pool, fsys); err != nil {
		t.Fatal(err)
	}
	if got, want := count(t, pool, "select count(*) from goose_db_version"), len(fsys)+1; got != want {
		t.Errorf("a second run changed goose_db_version to %d rows", got)
	}
}

func TestANewMigrationAfterTheBaselineIsTheOnlyOneThatRuns(t *testing.T) {
	pool := testutil.NewDB(t)
	fsys := files(t)
	asSqlxMigrated(t, pool, fsys)
	fsys["99999999999999_extra_table.sql"] = &fstest.MapFile{Data: []byte("-- +goose Up\ncreate table extra_table (id int);\n")}
	if err := migrate.Run(context.Background(), pool, fsys); err != nil {
		t.Fatal(err)
	}
	if !exists(t, pool, "extra_table") {
		t.Error("the new migration did not run")
	}
	if got, want := count(t, pool, "select count(*) from goose_db_version"), len(fsys)+1; got != want {
		t.Errorf("goose_db_version has %d rows, want %d", got, want)
	}
}

func TestTheBaselineRefusesADatabaseItCannotVouchFor(t *testing.T) {
	for _, c := range []struct {
		name    string
		tamper  string
		wantErr string
	}{
		{"an unknown version", `insert into _sqlx_migrations (version, description, success, checksum, execution_time) values (20270101000000, 'from the future', true, '\x00', 1)`, "unknown to this binary"},
		{"a failed migration", `update _sqlx_migrations set success = false where version = 20261028120000`, "failed"},
		{"a renamed migration", `update _sqlx_migrations set description = 'something else' where version = 20261028120100`, "this binary's file is"},
	} {
		t.Run(c.name, func(t *testing.T) {
			pool := testutil.NewDB(t)
			fsys := files(t)
			asSqlxMigrated(t, pool, fsys)
			if _, err := pool.Exec(context.Background(), c.tamper); err != nil {
				t.Fatal(err)
			}
			err := migrate.Run(context.Background(), pool, fsys)
			if err == nil || !strings.Contains(err.Error(), c.wantErr) {
				t.Fatalf("got %v, want an error containing %q", err, c.wantErr)
			}
			if exists(t, pool, "goose_db_version") {
				t.Error("a refused baseline must not leave a goose table behind")
			}
		})
	}
}

func TestAGooseTableThatDisagreesWithTheOldOneIsRefused(t *testing.T) {
	pool := testutil.NewDB(t)
	fsys := files(t)
	asSqlxMigrated(t, pool, fsys)
	ctx := context.Background()
	if err := migrate.Run(ctx, pool, fsys); err != nil { // baseline once
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `delete from goose_db_version where version_id = 20261028120300`); err != nil {
		t.Fatal(err)
	}
	err := migrate.Run(ctx, pool, fsys)
	if err == nil || !strings.Contains(err.Error(), "disagrees") {
		t.Fatalf("got %v, want a disagreement error", err)
	}
}

func TestSimultaneousStartsApplyEachMigrationOnce(t *testing.T) {
	pool := testutil.NewEmptyDB(t)
	fsys := files(t)
	var wg sync.WaitGroup
	errs := make(chan error, 6)
	for range 6 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			errs <- migrate.Run(context.Background(), pool, fsys)
		}()
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		if err != nil {
			t.Error(err)
		}
	}
	if got, want := count(t, pool, "select count(*) from goose_db_version"), len(fsys)+1; got != want {
		t.Errorf("goose_db_version has %d rows, want %d (each version exactly once)", got, want)
	}
	if dup := count(t, pool, "select count(*) from (select version_id from goose_db_version group by 1 having count(*) > 1) d"); dup != 0 {
		t.Errorf("%d versions were applied twice", dup)
	}
}
