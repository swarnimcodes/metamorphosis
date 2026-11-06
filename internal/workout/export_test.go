package workout

import "context"

// NewTestService builds a service whose "transaction" just runs fn on the store.
func NewTestService(store Store) *Service {
	return &Service{store: store, tx: func(_ context.Context, fn func(Store) error) error { return fn(store) }}
}
