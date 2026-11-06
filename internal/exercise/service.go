// Package exercise is the exercise catalog: searching it by name or alias (pulling variation tags out of the query)
// and listing the tags.
//
// Search is two queries glued together here. The first matches the query against names and aliases, ranked by
// exact match, then substring, then trigram similarity. If the query is not itself an exact name, tag words are
// extracted from it and the rest is searched again, with the found tags suggested on the results.
package exercise

import (
	"bytes"
	"context"
	"slices"
	"sort"
	"strings"
	"unicode"

	"github.com/google/uuid"

	"github.com/smarniw/metamorphosis/internal/db"
)

const (
	defaultLimit = 20
	maxLimit     = 50
)

// Exercise is a search hit. SuggestedTagIDs is never nil.
type Exercise struct {
	ID              uuid.UUID
	Slug            *string
	Name            string
	Kind            string
	Status          string
	SuggestedTagIDs []uuid.UUID
}

// Tag is a variation tag.
type Tag struct {
	ID       uuid.UUID
	Slug     string
	Name     string
	Category string
}

// Store is the database access the service needs. *db.Queries satisfies it. Every statement is static SQL in
// queries/exercises.sql: nothing here needs a dynamic WHERE or ORDER BY.
type Store interface {
	SearchExercises(ctx context.Context, arg db.SearchExercisesParams) ([]db.SearchExercisesRow, error)
	ExercisesSuggestingTags(ctx context.Context, arg db.ExercisesSuggestingTagsParams) ([]db.ExercisesSuggestingTagsRow, error)
	ExerciseTagLinks(ctx context.Context, arg db.ExerciseTagLinksParams) ([]db.ExerciseVariationTag, error)
	TagNames(ctx context.Context) ([]db.TagNamesRow, error)
	ListTags(ctx context.Context) ([]db.ListTagsRow, error)
}

type Service struct{ store Store }

func NewService(store Store) *Service { return &Service{store: store} }

// hit is a row of either search query.
type hit struct {
	ID     uuid.UUID
	Slug   *string
	Name   string
	Kind   string
	Status string
	// The normalised query equals this exercise's name or one of its aliases.
	Exact bool
}

func (h hit) exercise(tags []uuid.UUID) Exercise {
	if tags == nil {
		tags = []uuid.UUID{}
	}
	return Exercise{h.ID, h.Slug, h.Name, h.Kind, h.Status, tags}
}

func (s *Service) byName(ctx context.Context, user uuid.UUID, q string, limit int64) ([]hit, error) {
	rows, err := s.store.SearchExercises(ctx, db.SearchExercisesParams{
		Q: q, UserID: uuid.NullUUID{UUID: user, Valid: true}, MaxRows: rows32(limit),
	})
	if err != nil {
		return nil, err
	}
	hits := make([]hit, 0, len(rows))
	for _, r := range rows {
		hits = append(hits, hit(r))
	}
	return hits, nil
}

// suggesting lists the exercises the catalog suggests any of tagIDs for.
func (s *Service) suggesting(ctx context.Context, user uuid.UUID, tagIDs []uuid.UUID, limit int64) ([]hit, error) {
	rows, err := s.store.ExercisesSuggestingTags(ctx, db.ExercisesSuggestingTagsParams{
		TagIds: tagIDs, UserID: uuid.NullUUID{UUID: user, Valid: true}, MaxRows: rows32(limit),
	})
	if err != nil {
		return nil, err
	}
	hits := make([]hit, 0, len(rows))
	for _, r := range rows {
		hits = append(hits, hit(r))
	}
	return hits, nil
}

// Search finds exercises for a query; limit (nil: 20) is clamped to 1..50. If the whole query is not already an
// exact name or alias, tag words are stripped out and searched separately, so "preacher curl" finds Bicep Curl
// with `preacher` suggested.
func (s *Service) Search(ctx context.Context, user uuid.UUID, q string, limit *int64) ([]Exercise, error) {
	n := int64(defaultLimit)
	if limit != nil {
		n = min(max(*limit, 1), maxLimit)
	}
	full, err := s.byName(ctx, user, q, n)
	if err != nil {
		return nil, err
	}
	plain := func(hits []hit) []Exercise {
		out := make([]Exercise, 0, len(hits))
		for _, h := range hits {
			out = append(out, h.exercise(nil))
		}
		return out
	}

	queryTokens := tokens(q)
	if len(queryTokens) == 0 || slices.ContainsFunc(full, func(h hit) bool { return h.Exact }) {
		return plain(full), nil
	}

	trows, err := s.store.TagNames(ctx)
	if err != nil {
		return nil, err
	}
	tags := make([]phrase, 0, len(trows))
	for _, t := range trows {
		tags = append(tags, phrase{t.ID, tokens(t.Name)})
	}
	found, rest := extractTags(queryTokens, tags)
	if len(found) == 0 {
		return plain(full), nil
	}

	onlyTagWords := len(rest) == 0
	var stripped []hit
	if onlyTagWords {
		stripped, err = s.suggesting(ctx, user, found, n)
	} else {
		stripped, err = s.byName(ctx, user, strings.Join(rest, " "), n)
	}
	if err != nil {
		return nil, err
	}

	// A tag is only suggested where it makes sense: on an exact match of the remaining words, or where the
	// catalog lists it for that exercise.
	ids := make([]uuid.UUID, 0, len(stripped))
	for _, h := range stripped {
		ids = append(ids, h.ID)
	}
	type link struct{ ex, tag uuid.UUID }
	linked := map[link]bool{}
	links, err := s.store.ExerciseTagLinks(ctx, db.ExerciseTagLinksParams{ExerciseIds: ids, TagIds: found})
	if err != nil {
		return nil, err
	}
	for _, l := range links {
		linked[link{l.ExerciseID, l.TagID}] = true
	}

	byID := make(map[uuid.UUID]Exercise, len(stripped))
	strippedOut := make([]Exercise, 0, len(stripped))
	for _, h := range stripped {
		sug := []uuid.UUID{}
		for _, t := range found {
			if h.Exact || linked[link{h.ID, t}] {
				sug = append(sug, t)
			}
		}
		o := h.exercise(sug)
		strippedOut = append(strippedOut, o)
		byID[h.ID] = o
	}

	out := []Exercise{}
	if onlyTagWords {
		// The user may be mid-way through an exercise name ("overhead"), so name matches lead, carrying the
		// suggestion if the catalog lists the tag for them.
		for _, h := range full {
			var tags []uuid.UUID
			if o, ok := byID[h.ID]; ok {
				tags = o.SuggestedTagIDs
				delete(byID, h.ID)
			}
			out = append(out, h.exercise(tags))
		}
		tagOnly := make([]Exercise, 0, len(byID))
		for _, o := range byID {
			tagOnly = append(tagOnly, o)
		}
		sort.Slice(tagOnly, func(i, j int) bool {
			if tagOnly[i].Name != tagOnly[j].Name {
				return tagOnly[i].Name < tagOnly[j].Name
			}
			return bytes.Compare(tagOnly[i].ID[:], tagOnly[j].ID[:]) < 0
		})
		out = append(out, tagOnly...)
	} else {
		seen := map[uuid.UUID]bool{}
		for _, o := range strippedOut {
			seen[o.ID] = true
		}
		out = append(out, strippedOut...)
		for _, h := range full {
			if !seen[h.ID] {
				out = append(out, h.exercise(nil))
			}
		}
	}
	if int64(len(out)) > n {
		out = out[:n]
	}
	return out, nil
}

// Tags lists every variation tag, by category then name.
func (s *Service) Tags(ctx context.Context) ([]Tag, error) {
	rows, err := s.store.ListTags(ctx)
	if err != nil {
		return nil, err
	}
	out := make([]Tag, 0, len(rows))
	for _, r := range rows {
		out = append(out, Tag(r))
	}
	return out, nil
}

// rows32 narrows a limit that is already clamped to 1..50.
func rows32(limit int64) int32 { return int32(limit) } //nolint:gosec // clamped in Search

func tokens(text string) []string {
	return strings.FieldsFunc(strings.ToLower(text), func(c rune) bool {
		return !unicode.IsLetter(c) && !unicode.IsNumber(c)
	})
}

type phrase struct {
	id    uuid.UUID
	words []string
}

type partialMatch struct {
	nameLen int
	tag     uuid.UUID
	start   int
	words   int
}

// extractTags finds variation tags named inside a query. Longer tag names win ("ez bar" before "bar"), and each
// query word is used at most once. Words may be partial tag names, since search runs while typing.
// Returns the tag ids found and the query words left over.
func extractTags(query []string, tags []phrase) ([]uuid.UUID, []string) {
	var phrases []phrase
	for _, t := range tags {
		if len(t.words) > 0 {
			phrases = append(phrases, t)
		}
	}
	sort.SliceStable(phrases, func(i, j int) bool { return len(phrases[i].words) > len(phrases[j].words) })

	used := make([]bool, len(query))
	found := []uuid.UUID{}
	allFree := func(from, to int) bool {
		for _, u := range used[from:to] {
			if u {
				return false
			}
		}
		return true
	}
	mark := func(from, to int) {
		for i := from; i < to; i++ {
			used[i] = true
		}
	}

	// Whole words first, so a complete tag always beats a partial match.
	for _, p := range phrases {
		n := len(p.words)
		if n > len(query) {
			continue
		}
		for i := 0; i <= len(query)-n; i++ {
			if slices.Equal(query[i:i+n], p.words) && allFree(i, i+n) {
				mark(i, i+n)
				found = append(found, p.id)
				break
			}
		}
	}

	// Then partial words. Three letters are needed to guess a single-word tag; a multi-word tag is already
	// anchored by the words before its last. Where several tags fit, the shortest name wins.
	var partial []partialMatch
	for _, p := range phrases {
		n := len(p.words)
		if slices.Contains(found, p.id) || n > len(query) {
			continue
		}
		head, tail := p.words[:n-1], p.words[n-1]
		minLen := 1
		if n == 1 {
			minLen = 3
		}
		for start := 0; start <= len(query)-n; start++ {
			word := query[start+n-1]
			if slices.Equal(query[start:start+n-1], head) && len(word) >= minLen && strings.HasPrefix(tail, word) {
				partial = append(partial, partialMatch{len(strings.Join(p.words, " ")), p.id, start, n})
			}
		}
	}
	sort.Slice(partial, func(i, j int) bool {
		a, b := partial[i], partial[j]
		if a.nameLen != b.nameLen {
			return a.nameLen < b.nameLen
		}
		if c := bytes.Compare(a.tag[:], b.tag[:]); c != 0 {
			return c < 0
		}
		if a.start != b.start {
			return a.start < b.start
		}
		return a.words < b.words
	})
	for _, p := range partial {
		if !slices.Contains(found, p.tag) && allFree(p.start, p.start+p.words) {
			mark(p.start, p.start+p.words)
			found = append(found, p.tag)
		}
	}

	rest := []string{}
	for i, t := range query {
		if !used[i] {
			rest = append(rest, t)
		}
	}
	return found, rest
}
