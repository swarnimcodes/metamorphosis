// Package migrate applies the repository's SQL migrations using sqlx's bookkeeping table, so a database
// that the Rust API migrated stays valid and vice versa: same table, same SHA-384 checksums.
package migrate

import (
	"bytes"
	"context"
	"crypto/sha512"
	"fmt"
	"io/fs"
	"log/slog"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

var fileName = regexp.MustCompile(`^(\d+)_(.+)\.sql$`)

const createTable = `
create table if not exists _sqlx_migrations (
    version bigint primary key,
    description text not null,
    installed_on timestamptz not null default now(),
    success boolean not null,
    checksum bytea not null,
    execution_time bigint not null
)`

// A fixed key: only migrators of this API compete for it.
const lockKey = 7_424_239_913

type migration struct {
	version     int64
	description string
	sql         string
	checksum    []byte
}

func load(files fs.FS) ([]migration, error) {
	entries, err := fs.ReadDir(files, ".")
	if err != nil {
		return nil, err
	}
	var out []migration
	for _, e := range entries {
		m := fileName.FindStringSubmatch(e.Name())
		if m == nil {
			continue
		}
		version, _ := strconv.ParseInt(m[1], 10, 64)
		body, err := fs.ReadFile(files, e.Name())
		if err != nil {
			return nil, err
		}
		sum := sha512.Sum384(body)
		out = append(out, migration{version, strings.ReplaceAll(m[2], "_", " "), string(body), sum[:]})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].version < out[j].version })
	return out, nil
}

// Latest returns the newest version in the embedded files.
func Latest(files fs.FS) (int64, error) {
	ms, err := load(files)
	if err != nil || len(ms) == 0 {
		return 0, err
	}
	return ms[len(ms)-1].version, nil
}

// Run applies every migration the database has not seen, in order, each in its own transaction. Applied
// migrations whose file changed, or that no longer exist, stop the run, like sqlx does.
func Run(ctx context.Context, pool *pgxpool.Pool, files fs.FS) error {
	ms, err := load(files)
	if err != nil {
		return err
	}
	conn, err := pool.Acquire(ctx)
	if err != nil {
		return err
	}
	defer conn.Release()

	if _, err := conn.Exec(ctx, "select pg_advisory_lock($1)", lockKey); err != nil {
		return err
	}
	defer conn.Exec(context.WithoutCancel(ctx), "select pg_advisory_unlock($1)", lockKey) //nolint:errcheck

	if _, err := conn.Exec(ctx, createTable); err != nil {
		return err
	}
	rows, err := conn.Query(ctx, "select version, checksum from _sqlx_migrations where success order by version")
	if err != nil {
		return err
	}
	applied := map[int64][]byte{}
	for rows.Next() {
		var v int64
		var sum []byte
		if err := rows.Scan(&v, &sum); err != nil {
			return err
		}
		applied[v] = sum
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return err
	}

	known := map[int64]bool{}
	for _, m := range ms {
		known[m.version] = true
		if sum, done := applied[m.version]; done && !bytes.Equal(sum, m.checksum) {
			return fmt.Errorf("migration %d was applied but its file has changed", m.version)
		}
	}
	for v := range applied {
		if !known[v] {
			return fmt.Errorf("migration %d is applied in the database but missing from this binary", v)
		}
	}

	for _, m := range ms {
		if _, done := applied[m.version]; done {
			continue
		}
		start := time.Now()
		tx, err := conn.Begin(ctx)
		if err != nil {
			return err
		}
		// PgConn().Exec uses the simple protocol, which allows a file with many statements.
		if _, err := tx.Conn().PgConn().Exec(ctx, m.sql).ReadAll(); err != nil {
			tx.Rollback(ctx) //nolint:errcheck
			return fmt.Errorf("migration %d (%s): %w", m.version, m.description, err)
		}
		if _, err := tx.Exec(ctx,
			`insert into _sqlx_migrations (version, description, success, checksum, execution_time)
			 values ($1, $2, true, $3, $4)`,
			m.version, m.description, m.checksum, time.Since(start).Nanoseconds()); err != nil {
			tx.Rollback(ctx) //nolint:errcheck
			return err
		}
		if err := tx.Commit(ctx); err != nil {
			return err
		}
		slog.Info("applied migration", "version", m.version, "description", m.description)
	}
	return nil
}
