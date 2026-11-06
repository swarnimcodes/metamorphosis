// Package gym is the shared gym directory: searching it (the caller's recent gyms first) and adding to it, with a
// guard against near-duplicates.
package gym

import (
	"context"
	"strings"
	"unicode"

	"github.com/google/uuid"

	"github.com/smarniw/metamorphosis/internal/db"
	"github.com/smarniw/metamorphosis/internal/domain"
)

const (
	defaultLimit = 20
	maxLimit     = 50
	// MaxSimilar is the most near-duplicates a refusal lists.
	MaxSimilar = 10
)

// Gym is a directory entry. The JSON tags are the shape of the entries in the duplicate refusal's `similar`.
type Gym struct {
	ID       uuid.UUID `json:"id"`
	Name     string    `json:"name"`
	Chain    *string   `json:"chain"`
	Area     *string   `json:"area"`
	Address  *string   `json:"address"`
	City     *string   `json:"city"`
	Lat      *float64  `json:"lat"`
	Lng      *float64  `json:"lng"`
	MapsURL  *string   `json:"maps_url"`
	Verified bool      `json:"verified"`
}

// Store is the database access the service needs. *db.Queries satisfies it.
type Store interface {
	SearchGyms(ctx context.Context, arg db.SearchGymsParams) ([]db.SearchGymsRow, error)
	SimilarGyms(ctx context.Context, arg db.SimilarGymsParams) ([]db.SimilarGymsRow, error)
	InsertGym(ctx context.Context, arg db.InsertGymParams) (db.InsertGymRow, error)
}

type Service struct{ store Store }

func NewService(store Store) *Service { return &Service{store: store} }

// Search lists gyms matching q, best match first and then the caller's most recently used, then verified, then by
// name. limit (nil: 20) is clamped to 1..50.
func (s *Service) Search(ctx context.Context, user uuid.UUID, q string, limit *int64) ([]Gym, error) {
	rows, err := s.store.SearchGyms(ctx, db.SearchGymsParams{Q: q, UserID: user, MaxRows: clampLimit(limit)})
	if err != nil {
		return nil, err
	}
	out := make([]Gym, 0, len(rows))
	for _, r := range rows {
		out = append(out, fromRow(db.InsertGymRow(r)))
	}
	return out, nil
}

// fromRow converts any of the queries' identical gym rows (the three row types have the same columns).
func fromRow(r db.InsertGymRow) Gym {
	return Gym{
		ID: r.ID, Name: r.Name, Chain: r.Chain, Area: r.Area, Address: r.Address, City: r.City,
		Lat: r.Lat, Lng: r.Lng, MapsURL: r.MapsUrl, Verified: r.Verified,
	}
}

func clampLimit(limit *int64) int32 {
	if limit == nil {
		return defaultLimit
	}
	return int32(min(max(*limit, 1), maxLimit)) //nolint:gosec // clamped to 1..50
}

// NewGym is a request to add a gym; the optional fields may be nil or blank.
type NewGym struct {
	Name, Chain, Area, Address, City, MapsURL *string
	// Force skips the near-duplicate guard.
	Force bool
}

// Create adds a gym. Unless in.Force is set it refuses (409, listing the candidates in `similar`) when a gym with a
// similar name exists in the same city, or in any city if none is given.
func (s *Service) Create(ctx context.Context, user uuid.UUID, in NewGym) (Gym, error) {
	name := ""
	if in.Name != nil {
		name = strings.TrimSpace(*in.Name)
	}
	if name == "" {
		return Gym{}, domain.Invalid("name must not be empty")
	}
	chain, area, address, city := clean(in.Chain), clean(in.Area), clean(in.Address), clean(in.City)
	mapsURL := clean(in.MapsURL)
	if mapsURL != nil && !validMapsURL(*mapsURL) {
		return Gym{}, domain.Invalid("maps_url must be a link to Google Maps, Apple Maps or OpenStreetMap")
	}

	if !in.Force {
		rows, err := s.store.SimilarGyms(ctx, db.SimilarGymsParams{Name: name, City: city, MaxRows: MaxSimilar})
		if err != nil {
			return Gym{}, err
		}
		if len(rows) > 0 {
			similar := make([]Gym, 0, len(rows))
			for _, r := range rows {
				similar = append(similar, fromRow(db.InsertGymRow(r)))
			}
			return Gym{}, domain.ConflictWith("similar gyms exist", map[string]any{"similar": similar})
		}
	}

	row, err := s.store.InsertGym(ctx, db.InsertGymParams{
		Name: name, Chain: chain, Area: area, Address: address, City: city, MapsUrl: mapsURL,
		CreatedBy: uuid.NullUUID{UUID: user, Valid: true},
	})
	if err != nil {
		return Gym{}, err
	}
	return fromRow(row), nil
}

// clean trims an optional string; blank means absent.
func clean(v *string) *string {
	if v == nil {
		return nil
	}
	t := strings.TrimSpace(*v)
	if t == "" {
		return nil
	}
	return &t
}

// validMapsURL: only links to a known maps service are kept. The directory is shared, so a stored link is opened
// by other people and must not be an arbitrary site.
func validMapsURL(url string) bool {
	rest, ok := strings.CutPrefix(url, "https://")
	if !ok {
		return false
	}
	host, path, _ := strings.Cut(rest, "/")
	switch host {
	case "maps.app.goo.gl", "goo.gl", "maps.google.com", "www.google.com", "google.com", "maps.apple.com", "www.openstreetmap.org":
	default:
		return false
	}
	if strings.IndexFunc(url, func(c rune) bool { return unicode.IsSpace(c) || unicode.IsControl(c) }) >= 0 {
		return false
	}
	// google.com and goo.gl host other products; only their maps paths count.
	switch host {
	case "www.google.com", "google.com", "goo.gl":
		return strings.HasPrefix(path, "maps")
	}
	return true
}
