// Package gym is the shared gym directory: searching it (the caller's recent gyms first) and adding to it, with a
// guard against near-duplicates.
package gym

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"time"
	"unicode"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

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
	// CountryCode is ISO 3166-1 alpha-2, upper case.
	CountryCode *string `json:"country_code"`
	State       *string `json:"state"`
}

// Store is the database access the service needs. *db.Queries satisfies it.
type Store interface {
	SearchGyms(ctx context.Context, arg db.SearchGymsParams) ([]db.SearchGymsRow, error)
	SimilarGyms(ctx context.Context, arg db.SimilarGymsParams) ([]db.SimilarGymsRow, error)
	InsertGym(ctx context.Context, arg db.InsertGymParams) (db.InsertGymRow, error)
	GetGym(ctx context.Context, arg db.GetGymParams) (db.GetGymRow, error)
	LockGym(ctx context.Context, id uuid.UUID) (db.LockGymRow, error)
	UpdateGym(ctx context.Context, arg db.UpdateGymParams) error
	InsertGymEdit(ctx context.Context, arg db.InsertGymEditParams) error
	ListGymEdits(ctx context.Context, arg db.ListGymEditsParams) ([]db.ListGymEditsRow, error)
	ListGymChains(ctx context.Context, maxRows int32) ([]string, error)
}

var _ Store = (*db.Queries)(nil)

// Service holds the gym rules.
type Service struct {
	store Store
	// tx runs fn with a store bound to one transaction.
	tx func(ctx context.Context, fn func(Store) error) error
}

// NewService builds the service. inTx is db.InTx bound to the pool.
func NewService(store Store, inTx func(ctx context.Context, fn func(*db.Queries) error) error) *Service {
	return &Service{store: store, tx: func(ctx context.Context, fn func(Store) error) error {
		return inTx(ctx, func(q *db.Queries) error { return fn(q) })
	}}
}

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
		Lat: r.Lat, Lng: r.Lng, MapsURL: r.MapsUrl, Verified: r.Verified, CountryCode: r.CountryCode, State: r.State,
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
	Name, Chain, Area, Address, City, State, CountryCode, MapsURL *string
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
	f, err := validateFields(fields{
		Name: name, Chain: clean(in.Chain), Area: clean(in.Area), Address: clean(in.Address), City: clean(in.City),
		State: clean(in.State), CountryCode: clean(in.CountryCode), MapsURL: clean(in.MapsURL),
	})
	if err != nil {
		return Gym{}, err
	}
	chain, area, address, city := f.Chain, f.Area, f.Address, f.City

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
		Name: f.Name, Chain: chain, Area: area, Address: address, City: city, State: f.State, CountryCode: f.CountryCode,
		MapsUrl:   f.MapsURL,
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

// maxFieldLen caps every text field: the directory is shared, so nobody gets to store an essay in a city name.
const maxFieldLen = 200

// fields are a gym's editable columns, already trimmed (nil = blank).
type fields struct {
	Name                                                    string
	Chain, Area, Address, City, State, CountryCode, MapsURL *string
}

// validateFields applies the rules that hold for a new gym and for an edited one.
func validateFields(f fields) (fields, error) {
	if len(f.Name) > maxFieldLen {
		return fields{}, domain.Invalid("name is too long")
	}
	for label, v := range map[string]*string{
		"chain": f.Chain, "area": f.Area, "address": f.Address, "city": f.City, "state": f.State,
	} {
		if v != nil && len(*v) > maxFieldLen {
			return fields{}, domain.Invalid("%s is too long", label)
		}
	}
	if f.CountryCode != nil {
		cc := strings.ToUpper(*f.CountryCode)
		if len(cc) != 2 || strings.IndexFunc(cc, func(c rune) bool { return c < 'A' || c > 'Z' }) >= 0 {
			return fields{}, domain.Invalid("country_code must be two letters (ISO 3166-1 alpha-2)")
		}
		f.CountryCode = &cc
	}
	if f.MapsURL != nil && !validMapsURL(*f.MapsURL) {
		return fields{}, domain.Invalid("maps_url must be a link to Google Maps, Apple Maps or OpenStreetMap")
	}
	return f, nil
}

// Detail is one gym with the caller's history there.
type Detail struct {
	Gym
	// Visits is how many of the caller's workouts were logged here; LastVisit is the latest one's start.
	Visits    int32
	LastVisit *time.Time
	// Editable is false for verified gyms: the seeded directory entries are curated, not crowd-edited.
	Editable bool
}

func notFound(err error) error {
	if errors.Is(err, pgx.ErrNoRows) {
		return domain.NotFound()
	}
	return err
}

// Get returns one gym and the caller's visits to it.
func (s *Service) Get(ctx context.Context, user, id uuid.UUID) (Detail, error) {
	r, err := s.store.GetGym(ctx, db.GetGymParams{ID: id, UserID: user})
	if err != nil {
		return Detail{}, notFound(err)
	}
	g := Gym{
		ID: r.ID, Name: r.Name, Chain: r.Chain, Area: r.Area, Address: r.Address, City: r.City, Lat: r.Lat, Lng: r.Lng,
		MapsURL: r.MapsUrl, Verified: r.Verified, CountryCode: r.CountryCode, State: r.State,
	}
	d := Detail{Gym: g, Visits: r.Visits, Editable: !r.Verified}
	if r.Visits > 0 {
		d.LastVisit = &r.LastVisit
	}
	return d, nil
}

// Patch is a change to a gym. nil leaves a field as it is; a blank string clears an optional one.
type Patch struct {
	Name, Chain, Area, Address, City, State, CountryCode, MapsURL *string
}

// Change is one field of an edit.
type Change struct {
	From *string `json:"from"`
	To   *string `json:"to"`
}

// Update edits a gym that is not verified and keeps a record of what changed. Anyone may edit: the history on the
// gym page is the check on that. An edit that changes nothing is not recorded.
func (s *Service) Update(ctx context.Context, user, id uuid.UUID, p Patch) (Gym, error) {
	var out Gym
	err := s.tx(ctx, func(st Store) error {
		cur, err := st.LockGym(ctx, id)
		if err != nil {
			return notFound(err)
		}
		if cur.Verified {
			return domain.InvalidCode("gym_locked", "verified gyms can't be edited")
		}
		old := fields{
			Name: cur.Name, Chain: cur.Chain, Area: cur.Area, Address: cur.Address, City: cur.City, State: cur.State,
			CountryCode: cur.CountryCode, MapsURL: cur.MapsUrl,
		}
		next := old
		if p.Name != nil {
			next.Name = strings.TrimSpace(*p.Name)
			if next.Name == "" {
				return domain.Invalid("name must not be empty")
			}
		}
		pick := func(patch, current *string) *string {
			if patch == nil {
				return current
			}
			return clean(patch)
		}
		next.Chain, next.Area, next.Address = pick(p.Chain, old.Chain), pick(p.Area, old.Area), pick(p.Address, old.Address)
		next.City, next.State = pick(p.City, old.City), pick(p.State, old.State)
		next.CountryCode, next.MapsURL = pick(p.CountryCode, old.CountryCode), pick(p.MapsURL, old.MapsURL)
		if next, err = validateFields(next); err != nil {
			return err
		}

		changes := map[string]Change{}
		note := func(field string, from, to *string) {
			if !sameText(from, to) {
				changes[field] = Change{From: from, To: to}
			}
		}
		name := func(v string) *string { return &v }
		note("name", name(old.Name), name(next.Name))
		note("chain", old.Chain, next.Chain)
		note("area", old.Area, next.Area)
		note("address", old.Address, next.Address)
		note("city", old.City, next.City)
		note("state", old.State, next.State)
		note("country_code", old.CountryCode, next.CountryCode)
		note("maps_url", old.MapsURL, next.MapsURL)

		out = Gym{
			ID: cur.ID, Name: next.Name, Chain: next.Chain, Area: next.Area, Address: next.Address, City: next.City,
			Lat: cur.Lat, Lng: cur.Lng, MapsURL: next.MapsURL, Verified: cur.Verified, CountryCode: next.CountryCode,
			State: next.State,
		}
		if len(changes) == 0 {
			return nil
		}
		if err := st.UpdateGym(ctx, db.UpdateGymParams{
			ID: id, Name: next.Name, Chain: next.Chain, Area: next.Area, Address: next.Address, City: next.City,
			State: next.State, CountryCode: next.CountryCode, MapsUrl: next.MapsURL,
		}); err != nil {
			return err
		}
		raw, err := json.Marshal(changes)
		if err != nil {
			return err
		}
		return st.InsertGymEdit(ctx, db.InsertGymEditParams{
			GymID: id, UserID: uuid.NullUUID{UUID: user, Valid: true}, Changes: raw,
		})
	})
	return out, err
}

func sameText(a, b *string) bool {
	if a == nil || b == nil {
		return a == b
	}
	return *a == *b
}

// Edit is one entry of a gym's history. The editor is only ever "you" or someone else.
type Edit struct {
	ID      uuid.UUID
	ByMe    bool
	At      time.Time
	Changes map[string]Change
}

// maxHistory is how many edits the gym page lists.
const maxHistory = 50

// History lists a gym's edits, newest first.
func (s *Service) History(ctx context.Context, user, id uuid.UUID) ([]Edit, error) {
	if _, err := s.store.GetGym(ctx, db.GetGymParams{ID: id, UserID: user}); err != nil {
		return nil, notFound(err)
	}
	rows, err := s.store.ListGymEdits(ctx, db.ListGymEditsParams{GymID: id, MaxRows: maxHistory})
	if err != nil {
		return nil, err
	}
	out := make([]Edit, 0, len(rows))
	for _, r := range rows {
		var ch map[string]Change
		if err := json.Unmarshal(r.Changes, &ch); err != nil {
			return nil, err
		}
		out = append(out, Edit{ID: r.ID, ByMe: r.UserID.Valid && r.UserID.UUID == user, At: r.CreatedAt, Changes: ch})
	}
	return out, nil
}

// maxChains is how many chain suggestions are offered.
const maxChains = 100

// Chains lists the chain names in the directory, most used first.
func (s *Service) Chains(ctx context.Context) ([]string, error) {
	out, err := s.store.ListGymChains(ctx, maxChains)
	if out == nil {
		out = []string{}
	}
	return out, err
}
