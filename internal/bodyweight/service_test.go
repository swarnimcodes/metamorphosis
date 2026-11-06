package bodyweight_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/shopspring/decimal"

	"github.com/smarniw/metamorphosis/internal/bodyweight"
	"github.com/smarniw/metamorphosis/internal/db"
	"github.com/smarniw/metamorphosis/internal/domain"
)

type fakeStore struct {
	latest   time.Time
	upserted []db.UpsertBodyWeightParams
	deleted  int64
	listed   db.ListBodyWeightsParams
}

func (f *fakeStore) ListBodyWeights(_ context.Context, a db.ListBodyWeightsParams) ([]db.ListBodyWeightsRow, error) {
	f.listed = a
	return nil, nil
}

func (f *fakeStore) UpsertBodyWeight(_ context.Context, a db.UpsertBodyWeightParams) (db.UpsertBodyWeightRow, error) {
	f.upserted = append(f.upserted, a)
	return db.UpsertBodyWeightRow{MeasuredOn: a.MeasuredOn, WeightKg: a.WeightKg}, nil
}

func (f *fakeStore) DeleteBodyWeight(context.Context, db.DeleteBodyWeightParams) (int64, error) {
	return f.deleted, nil
}

func (f *fakeStore) LatestLocalDate(context.Context) (time.Time, error) { return f.latest, nil }

func day(s string) time.Time {
	t, err := time.Parse("2006-01-02", s)
	if err != nil {
		panic(err)
	}
	return t
}

func isInvalid(err error) bool {
	var de *domain.Error
	return errors.As(err, &de) && de.Kind == domain.KindInvalid
}

func TestWeightsAreRoundedHalfToEvenAndBounded(t *testing.T) {
	for _, c := range []struct {
		in   string
		want string // "" means refused
	}{
		{"81.125", "81.12"}, {"81.135", "81.14"}, {"0.015", "0.02"}, {"0.025", "0.02"},
		{"85.4", "85.4"}, {"499.994", "499.99"},
		{"0.005", ""}, {"0.0049999", ""}, {"0", ""}, {"-3", ""}, {"499.995", ""}, {"500", ""}, {"1e3", ""},
	} {
		f := &fakeStore{latest: day("2026-11-05")}
		in, err := decimal.NewFromString(c.in)
		if err != nil {
			t.Fatal(err)
		}
		e, err := bodyweight.NewService(f).Set(context.Background(), uuid.New(), day("2026-11-01"), in)
		if c.want == "" {
			if !isInvalid(err) || len(f.upserted) != 0 {
				t.Errorf("%s: want a refusal and nothing stored, got err=%v stored=%d", c.in, err, len(f.upserted))
			}
			continue
		}
		if err != nil || e.WeightKg.String() != c.want {
			t.Errorf("%s: got %v (%v), want %s", c.in, e.WeightKg, err, c.want)
		}
	}
}

func TestADayAheadOfEveryTimezoneIsRefused(t *testing.T) {
	f := &fakeStore{latest: day("2026-11-05")}
	s := bodyweight.NewService(f)
	kg := decimal.NewFromInt(80)
	if _, err := s.Set(context.Background(), uuid.New(), day("2026-11-05"), kg); err != nil {
		t.Errorf("the latest local date itself is fine: %v", err)
	}
	if _, err := s.Set(context.Background(), uuid.New(), day("2026-11-06"), kg); !isInvalid(err) {
		t.Errorf("a date no timezone has reached yet must be refused, got %v", err)
	}
}

func TestListAsksForTheCapAndRemoveNeedsARow(t *testing.T) {
	f := &fakeStore{}
	s := bodyweight.NewService(f)
	out, err := s.List(context.Background(), uuid.New(), nil, nil)
	if err != nil || out == nil || len(out) != 0 {
		t.Errorf("an empty list must be an empty slice, not nil: %v %v", out, err)
	}
	if f.listed.MaxEntries != bodyweight.MaxEntries {
		t.Errorf("asked the store for %d, want %d", f.listed.MaxEntries, bodyweight.MaxEntries)
	}
	var de *domain.Error
	if err := s.Remove(context.Background(), uuid.New(), day("2026-01-01")); !errors.As(err, &de) || de.Kind != domain.KindNotFound {
		t.Errorf("removing nothing must be not-found, got %v", err)
	}
	f.deleted = 1
	if err := s.Remove(context.Background(), uuid.New(), day("2026-01-01")); err != nil {
		t.Errorf("removing a row: %v", err)
	}
}
