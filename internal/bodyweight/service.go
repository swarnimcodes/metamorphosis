// Package bodyweight is the daily weigh-in log: one entry per user per local calendar day.
//
// The client sends the date in the user's own timezone, so no timezone conversion happens here. A day cannot be
// later than the latest local date anywhere (UTC+14).
package bodyweight

import (
	"context"
	"time"

	"github.com/google/uuid"
	"github.com/shopspring/decimal"

	"github.com/smarniw/metamorphosis/internal/db"
	"github.com/smarniw/metamorphosis/internal/domain"
)

// MaxEntries is the most entries one list returns. Past it the oldest are dropped, never the newest.
const MaxEntries = 5000

var maxWeight = decimal.NewFromInt(500)

// Entry is one weigh-in.
type Entry struct {
	Day      time.Time
	WeightKg decimal.Decimal
}

// Store is the database access the service needs. *db.Queries satisfies it.
type Store interface {
	ListBodyWeights(ctx context.Context, arg db.ListBodyWeightsParams) ([]db.ListBodyWeightsRow, error)
	UpsertBodyWeight(ctx context.Context, arg db.UpsertBodyWeightParams) (db.UpsertBodyWeightRow, error)
	DeleteBodyWeight(ctx context.Context, arg db.DeleteBodyWeightParams) (int64, error)
	LatestLocalDate(ctx context.Context) (time.Time, error)
}

type Service struct{ store Store }

func NewService(store Store) *Service { return &Service{store: store} }

// List returns the user's weigh-ins between from and to (either may be nil), oldest first.
func (s *Service) List(ctx context.Context, user uuid.UUID, from, to *time.Time) ([]Entry, error) {
	rows, err := s.store.ListBodyWeights(ctx, db.ListBodyWeightsParams{
		UserID: user, FromDay: from, ToDay: to, MaxEntries: MaxEntries,
	})
	if err != nil {
		return nil, err
	}
	out := make([]Entry, 0, len(rows))
	for _, r := range rows {
		out = append(out, Entry{Day: r.MeasuredOn, WeightKg: r.WeightKg})
	}
	return out, nil
}

// Set records the weigh-in for day, replacing an earlier one. The weight is rounded half to even at two
// decimals and must then be above 0 and below 500.
func (s *Service) Set(ctx context.Context, user uuid.UUID, day time.Time, kg decimal.Decimal) (Entry, error) {
	kg = kg.RoundBank(2)
	if !kg.IsPositive() || kg.GreaterThanOrEqual(maxWeight) {
		return Entry{}, domain.Invalid("weight_kg must be between 0 and 500")
	}
	// The date is the person's own local date, from wherever their device is. The furthest ahead any local date
	// can be is UTC+14, so that is the limit: a traveller already on tomorrow's date is fine, and only a date
	// that cannot exist anywhere yet is refused.
	latest, err := s.store.LatestLocalDate(ctx)
	if err != nil {
		return Entry{}, err
	}
	if day.After(latest) {
		return Entry{}, domain.Invalid("date cannot be in the future")
	}
	row, err := s.store.UpsertBodyWeight(ctx, db.UpsertBodyWeightParams{UserID: user, MeasuredOn: day, WeightKg: kg})
	if err != nil {
		return Entry{}, err
	}
	return Entry{Day: row.MeasuredOn, WeightKg: row.WeightKg}, nil
}

// Remove deletes the user's weigh-in for day; there must be one.
func (s *Service) Remove(ctx context.Context, user uuid.UUID, day time.Time) error {
	n, err := s.store.DeleteBodyWeight(ctx, db.DeleteBodyWeightParams{UserID: user, MeasuredOn: day})
	if err != nil {
		return err
	}
	if n == 0 {
		return domain.NotFound()
	}
	return nil
}
