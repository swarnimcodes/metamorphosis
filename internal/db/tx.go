package db

import (
	"context"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// InTx runs fn with queries bound to one transaction: it commits if fn returns nil and rolls back otherwise.
// Services take it as a function field (`func(ctx context.Context, fn func(*Queries) error) error`) so tests can
// replace it.
func InTx(ctx context.Context, pool *pgxpool.Pool, fn func(*Queries) error) error {
	tx, err := pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback(ctx) }() // a no-op after a successful commit
	if err := fn(New(tx)); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

var _ = pgx.ErrNoRows // callers match no-row results with errors.Is(err, pgx.ErrNoRows)
