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
	ListGyms(ctx context.Context, arg db.ListGymsParams) ([]db.ListGymsRow, error)
	SimilarGyms(ctx context.Context, arg db.SimilarGymsParams) ([]db.SimilarGymsRow, error)
	InsertGym(ctx context.Context, arg db.InsertGymParams) (db.InsertGymRow, error)
	GetGym(ctx context.Context, arg db.GetGymParams) (db.GetGymRow, error)
	LockGym(ctx context.Context, id uuid.UUID) (db.LockGymRow, error)
	UpdateGym(ctx context.Context, arg db.UpdateGymParams) error
	InsertGymEdit(ctx context.Context, arg db.InsertGymEditParams) error
	GetGymEdit(ctx context.Context, arg db.GetGymEditParams) (db.GetGymEditRow, error)
	GymUserRole(ctx context.Context, id uuid.UUID) (db.UserRole, error)
	CountRecentGymEdits(ctx context.Context, arg db.CountRecentGymEditsParams) (int32, error)
	SetGymVerified(ctx context.Context, arg db.SetGymVerifiedParams) error
	SoftDeleteGym(ctx context.Context, id uuid.UUID) error
	MoveGymWorkouts(ctx context.Context, arg db.MoveGymWorkoutsParams) (int64, error)
	MoveUserGyms(ctx context.Context, arg db.MoveUserGymsParams) error
	DropUserGyms(ctx context.Context, gymID uuid.UUID) error
	MarkGymMerged(ctx context.Context, arg db.MarkGymMergedParams) error
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
	out := []Gym{}
	if strings.TrimSpace(q) == "" {
		// Nothing to search for: the directory in the caller's last-used order.
		rows, err := s.store.ListGyms(ctx, db.ListGymsParams{UserID: user, MaxRows: clampLimit(limit)})
		if err != nil {
			return nil, err
		}
		for _, r := range rows {
			out = append(out, fromRow(db.InsertGymRow(r)))
		}
		return out, nil
	}
	rows, err := s.store.SearchGyms(ctx, db.SearchGymsParams{Q: q, UserID: user, MaxRows: clampLimit(limit)})
	if err != nil {
		return nil, err
	}
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

// Detail is one gym with the caller's history there and what the caller may do to it.
type Detail struct {
	Gym
	// Visits is how many of the caller's workouts were logged here; LastVisit is the latest one's start.
	Visits    int32
	LastVisit *time.Time
	Can       Abilities
}

func notFound(err error) error {
	if errors.Is(err, pgx.ErrNoRows) {
		return domain.NotFound()
	}
	return err
}

// role is the caller's role. An account that no longer exists has none, which is the same as not being signed in.
func role(ctx context.Context, st Store, user uuid.UUID) (Role, error) {
	r, err := st.GymUserRole(ctx, user)
	if errors.Is(err, pgx.ErrNoRows) {
		return "", domain.Unauthorized()
	}
	return r, err
}

func gymFromLock(r db.LockGymRow) Gym { return fromRow(db.InsertGymRow(r)) }

// Get returns one gym, the caller's visits to it and what the caller may do to it.
func (s *Service) Get(ctx context.Context, user, id uuid.UUID) (Detail, error) {
	r, err := s.store.GetGym(ctx, db.GetGymParams{ID: id, UserID: user})
	if err != nil {
		return Detail{}, notFound(err)
	}
	who, err := role(ctx, s.store, user)
	if err != nil {
		return Detail{}, err
	}
	g := Gym{
		ID: r.ID, Name: r.Name, Chain: r.Chain, Area: r.Area, Address: r.Address, City: r.City, Lat: r.Lat, Lng: r.Lng,
		MapsURL: r.MapsUrl, Verified: r.Verified, CountryCode: r.CountryCode, State: r.State,
	}
	d := Detail{Gym: g, Visits: r.Visits, Can: AbilitiesFor(who, r.Verified)}
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

// Kinds of entry in a gym's history.
const (
	KindEdit     = "edit"
	KindVerify   = "verify"
	KindUnverify = "unverify"
	KindDelete   = "delete"
	KindMerge    = "merge"
	KindRevert   = "revert"
)

const (
	// maxEditsPerHour is how many edits an ordinary user may make in an hour. Moderators are not limited.
	maxEditsPerHour = 30
	editWindow      = time.Hour
)

// record writes one entry of a gym's history.
func record(ctx context.Context, st Store, gym uuid.UUID, user uuid.UUID, kind string, changes map[string]Change, revertOf uuid.NullUUID) error {
	if changes == nil {
		changes = map[string]Change{}
	}
	raw, err := json.Marshal(changes)
	if err != nil {
		return err
	}
	return st.InsertGymEdit(ctx, db.InsertGymEditParams{
		GymID: gym, UserID: uuid.NullUUID{UUID: user, Valid: true}, Kind: kind, Changes: raw, RevertOf: revertOf,
	})
}

// fieldOf reads a field of the editable set by its API name.
func fieldOf(f fields, name string) (*string, bool) {
	switch name {
	case "name":
		return &f.Name, true
	case "chain":
		return f.Chain, true
	case "area":
		return f.Area, true
	case "address":
		return f.Address, true
	case "city":
		return f.City, true
	case "state":
		return f.State, true
	case "country_code":
		return f.CountryCode, true
	case "maps_url":
		return f.MapsURL, true
	}
	return nil, false
}

// setField writes a field of the editable set by its API name.
func setField(f *fields, name string, v *string) {
	switch name {
	case "name":
		if v != nil {
			f.Name = *v
		}
	case "chain":
		f.Chain = v
	case "area":
		f.Area = v
	case "address":
		f.Address = v
	case "city":
		f.City = v
	case "state":
		f.State = v
	case "country_code":
		f.CountryCode = v
	case "maps_url":
		f.MapsURL = v
	}
}

func fieldsOf(r db.LockGymRow) fields {
	return fields{
		Name: r.Name, Chain: r.Chain, Area: r.Area, Address: r.Address, City: r.City, State: r.State,
		CountryCode: r.CountryCode, MapsURL: r.MapsUrl,
	}
}

func save(ctx context.Context, st Store, id uuid.UUID, f fields) error {
	return st.UpdateGym(ctx, db.UpdateGymParams{
		ID: id, Name: f.Name, Chain: f.Chain, Area: f.Area, Address: f.Address, City: f.City, State: f.State,
		CountryCode: f.CountryCode, MapsUrl: f.MapsURL,
	})
}

func withFields(cur db.LockGymRow, f fields) Gym {
	return Gym{
		ID: cur.ID, Name: f.Name, Chain: f.Chain, Area: f.Area, Address: f.Address, City: f.City, Lat: cur.Lat, Lng: cur.Lng,
		MapsURL: f.MapsURL, Verified: cur.Verified, CountryCode: f.CountryCode, State: f.State,
	}
}

// Update edits a gym and keeps a record of what changed. Whether the caller may depends on their role and on
// whether the gym is verified; ordinary users are rate limited. An edit that changes nothing is not recorded.
func (s *Service) Update(ctx context.Context, user, id uuid.UUID, p Patch) (Gym, error) {
	var out Gym
	err := s.tx(ctx, func(st Store) error {
		who, err := role(ctx, st, user)
		if err != nil {
			return err
		}
		cur, err := st.LockGym(ctx, id)
		if err != nil {
			return notFound(err)
		}
		if !AbilitiesFor(who, cur.Verified).Edit {
			return domain.Forbidden("gym_locked", "only moderators can edit verified gyms")
		}
		if !Staff(who) {
			n, err := st.CountRecentGymEdits(ctx, db.CountRecentGymEditsParams{UserID: uuid.NullUUID{UUID: user, Valid: true}, Since: time.Now().Add(-editWindow)})
			if err != nil {
				return err
			}
			if n >= maxEditsPerHour {
				return domain.RateLimited("too many gym edits, try again later")
			}
		}
		old := fieldsOf(cur)
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
		for _, name := range []string{"name", "chain", "area", "address", "city", "state", "country_code", "maps_url"} {
			from, _ := fieldOf(old, name)
			to, _ := fieldOf(next, name)
			if !sameText(from, to) {
				changes[name] = Change{From: from, To: to}
			}
		}
		out = withFields(cur, next)
		if len(changes) == 0 {
			return nil
		}
		if err := save(ctx, st, id, next); err != nil {
			return err
		}
		return record(ctx, st, id, user, KindEdit, changes, uuid.NullUUID{})
	})
	return out, err
}

func sameText(a, b *string) bool {
	if a == nil || b == nil {
		return a == b
	}
	return *a == *b
}

// SetVerified marks a gym verified or not. Moderators and admins only. Saying what is already so does nothing.
func (s *Service) SetVerified(ctx context.Context, user, id uuid.UUID, verified bool) (Gym, error) {
	var out Gym
	err := s.tx(ctx, func(st Store) error {
		who, err := role(ctx, st, user)
		if err != nil {
			return err
		}
		if !AbilitiesFor(who, false).Verify {
			return domain.Forbidden("forbidden", "only moderators can verify gyms")
		}
		cur, err := st.LockGym(ctx, id)
		if err != nil {
			return notFound(err)
		}
		out = gymFromLock(cur)
		out.Verified = verified
		if cur.Verified == verified {
			return nil
		}
		if err := st.SetGymVerified(ctx, db.SetGymVerifiedParams{ID: id, Verified: verified, By: uuid.NullUUID{UUID: user, Valid: true}}); err != nil {
			return err
		}
		kind := KindUnverify
		if verified {
			kind = KindVerify
		}
		return record(ctx, st, id, user, kind, nil, uuid.NullUUID{})
	})
	return out, err
}

// Remove deletes a gym (it is kept, hidden: workouts logged there keep their gym). Admins only.
func (s *Service) Remove(ctx context.Context, user, id uuid.UUID) error {
	return s.tx(ctx, func(st Store) error {
		who, err := role(ctx, st, user)
		if err != nil {
			return err
		}
		if !AbilitiesFor(who, false).Delete {
			return domain.Forbidden("forbidden", "only admins can delete gyms")
		}
		if _, err := st.LockGym(ctx, id); err != nil {
			return notFound(err)
		}
		if err := st.SoftDeleteGym(ctx, id); err != nil {
			return err
		}
		return record(ctx, st, id, user, KindDelete, nil, uuid.NullUUID{})
	})
}

// Merge folds a duplicate gym into another: its workouts and saved-gym entries move to the target and it is
// hidden. Moderators and admins only. Returns the target.
func (s *Service) Merge(ctx context.Context, user, sourceID, targetID uuid.UUID) (Gym, error) {
	if targetID == uuid.Nil {
		return Gym{}, domain.Invalid("missing field `into`")
	}
	if sourceID == targetID {
		return Gym{}, domain.Invalid("a gym can't be merged into itself")
	}
	var out Gym
	err := s.tx(ctx, func(st Store) error {
		who, err := role(ctx, st, user)
		if err != nil {
			return err
		}
		if !AbilitiesFor(who, false).Merge {
			return domain.Forbidden("forbidden", "only moderators can merge gyms")
		}
		// Lock in a fixed order so two merges in opposite directions cannot deadlock.
		first, second := sourceID, targetID
		if strings.Compare(first.String(), second.String()) > 0 {
			first, second = second, first
		}
		rows := map[uuid.UUID]db.LockGymRow{}
		for _, id := range []uuid.UUID{first, second} {
			r, err := st.LockGym(ctx, id)
			if err != nil {
				return notFound(err)
			}
			rows[id] = r
		}
		source, target := rows[sourceID], rows[targetID]
		src := uuid.NullUUID{UUID: sourceID, Valid: true}
		dst := uuid.NullUUID{UUID: targetID, Valid: true}
		if _, err := st.MoveGymWorkouts(ctx, db.MoveGymWorkoutsParams{SourceID: src, TargetID: dst}); err != nil {
			return err
		}
		if err := st.MoveUserGyms(ctx, db.MoveUserGymsParams{SourceID: sourceID, TargetID: targetID}); err != nil {
			return err
		}
		if err := st.DropUserGyms(ctx, sourceID); err != nil {
			return err
		}
		if err := st.MarkGymMerged(ctx, db.MarkGymMergedParams{ID: sourceID, TargetID: dst}); err != nil {
			return err
		}
		name := source.Name
		if err := record(ctx, st, targetID, user, KindMerge, map[string]Change{"merged_gym": {From: &name}}, uuid.NullUUID{}); err != nil {
			return err
		}
		out = gymFromLock(target)
		return nil
	})
	return out, err
}

// Revert undoes one edit by putting the old values back. Moderators and admins only. It refuses when a field the
// edit touched has been changed again since: undoing that edit would silently undo the later one too.
func (s *Service) Revert(ctx context.Context, user, gymID, editID uuid.UUID) (Gym, error) {
	var out Gym
	err := s.tx(ctx, func(st Store) error {
		who, err := role(ctx, st, user)
		if err != nil {
			return err
		}
		if !AbilitiesFor(who, false).Revert {
			return domain.Forbidden("forbidden", "only moderators can revert edits")
		}
		cur, err := st.LockGym(ctx, gymID)
		if err != nil {
			return notFound(err)
		}
		e, err := st.GetGymEdit(ctx, db.GetGymEditParams{ID: editID, GymID: gymID})
		if err != nil {
			return notFound(err)
		}
		if e.Kind != KindEdit {
			return domain.Invalid("only edits can be reverted")
		}
		if e.Reverted {
			return domain.Conflict("already_reverted", "this edit was already reverted")
		}
		var changes map[string]Change
		if err := json.Unmarshal(e.Changes, &changes); err != nil {
			return err
		}
		old := fieldsOf(cur)
		next := old
		inverse := map[string]Change{}
		for name, c := range changes {
			now, ok := fieldOf(old, name)
			if !ok {
				continue
			}
			if !sameText(now, c.To) {
				return domain.Conflict("changed_since", "a later edit changed this again; revert that one first")
			}
			setField(&next, name, c.From)
			inverse[name] = Change{From: c.To, To: c.From}
		}
		if next, err = validateFields(next); err != nil {
			return err
		}
		if err := save(ctx, st, gymID, next); err != nil {
			return err
		}
		out = withFields(cur, next)
		return record(ctx, st, gymID, user, KindRevert, inverse, uuid.NullUUID{UUID: editID, Valid: true})
	})
	return out, err
}

// Edit is one entry of a gym's history. Only moderators are told who made it; everyone else just sees "you" or
// "a member".
type Edit struct {
	ID   uuid.UUID
	Kind string
	ByMe bool
	// By is the editor's display name, set only for a moderator or admin reading.
	By      *string
	At      time.Time
	Changes map[string]Change
	// Reverted: a later entry undid this one. Revertible: the caller may undo it now.
	Reverted, Revertible bool
}

// maxHistory is how many entries the gym page lists.
const maxHistory = 50

// History lists a gym's history, newest first.
func (s *Service) History(ctx context.Context, user, id uuid.UUID) ([]Edit, error) {
	if _, err := s.store.GetGym(ctx, db.GetGymParams{ID: id, UserID: user}); err != nil {
		return nil, notFound(err)
	}
	who, err := role(ctx, s.store, user)
	if err != nil {
		return nil, err
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
		e := Edit{
			ID: r.ID, Kind: r.Kind, ByMe: r.UserID.Valid && r.UserID.UUID == user, At: r.CreatedAt, Changes: ch,
			Reverted: r.Reverted, Revertible: Staff(who) && r.Kind == KindEdit && !r.Reverted,
		}
		if Staff(who) {
			e.By = r.EditorName
		}
		out = append(out, e)
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
