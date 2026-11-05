package api

import (
	"bytes"
	"context"
	"errors"
	"github.com/go-chi/chi/v5"
	"net/http"
	"slices"
	"sort"
	"strconv"
	"strings"
	"unicode"

	"github.com/google/uuid"

	"github.com/smarniw/metamorphosis/internal/httpx"
)

func (s *Server) exercisesRoutes(r chi.Router) {
	h := httpx.Handle
	r.MethodFunc("GET", "/exercises", h(s.authed(s.searchExercises)))
	r.MethodFunc("GET", "/tags", h(s.authed(s.listTags)))
}

type exerciseOut struct {
	ID              uuid.UUID   `json:"id"`
	Slug            *string     `json:"slug"`
	Name            string      `json:"name"`
	Kind            string      `json:"kind"`
	Status          string      `json:"status"`
	SuggestedTagIDs []uuid.UUID `json:"suggested_tag_ids"`
}

type exerciseHit struct {
	ID     uuid.UUID
	Slug   *string
	Name   string
	Kind   string
	Status string
	// The normalised query equals this exercise's name or one of its aliases.
	Exact bool
}

func (h exerciseHit) out(tags []uuid.UUID) exerciseOut {
	if tags == nil {
		tags = []uuid.UUID{}
	}
	return exerciseOut{h.ID, h.Slug, h.Name, h.Kind, h.Status, tags}
}

// exercisesLimit parses ?limit= like axum's Query<Option<i64>> and clamps it to 1..=50. A bad value is axum's
// plain-text 400 rejection (not the API's JSON shape), so it is written here and ok is false.
func exercisesLimit(w http.ResponseWriter, r *http.Request) (limit int64, ok bool) {
	v, present := r.URL.Query()["limit"]
	if !present {
		return 20, true
	}
	n, err := strconv.ParseInt(v[0], 10, 64)
	if err != nil {
		reason := "invalid digit found in string"
		switch {
		case v[0] == "":
			reason = "cannot parse integer from empty string"
		case errors.Is(err, strconv.ErrRange):
			reason = "number too large to fit in target type"
			if strings.HasPrefix(v[0], "-") {
				reason = "number too small to fit in target type"
			}
		}
		w.Header().Set("Content-Type", "text/plain; charset=utf-8")
		w.WriteHeader(http.StatusBadRequest)
		_, _ = w.Write([]byte("Failed to deserialize query string: limit: " + reason))
		return 0, false
	}
	return min(max(n, 1), 50), true
}

func exercisesTokens(text string) []string {
	return strings.FieldsFunc(strings.ToLower(text), func(c rune) bool {
		return !unicode.IsLetter(c) && !unicode.IsNumber(c)
	})
}

type exercisesPhrase struct {
	id    uuid.UUID
	words []string
}

type exercisesPartial struct {
	nameLen int
	tag     uuid.UUID
	start   int
	words   int
}

// exercisesExtractTags finds variation tags named inside a query. Longer tag names win ("ez bar" before "bar"),
// and each query word is used at most once. Words may be partial tag names, since search runs while typing.
// Returns the tag ids found and the query words left over.
func exercisesExtractTags(query []string, tags []exercisesPhrase) ([]uuid.UUID, []string) {
	var phrases []exercisesPhrase
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
	var partial []exercisesPartial
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
				partial = append(partial, exercisesPartial{len(strings.Join(p.words, " ")), p.id, start, n})
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

func (s *Server) exercisesScan(ctx context.Context, sql string, args ...any) ([]exerciseHit, error) {
	rows, err := s.DB.Query(ctx, sql, args...)
	if err != nil {
		return nil, httpx.FromDB(err)
	}
	defer rows.Close()
	hits := []exerciseHit{}
	for rows.Next() {
		var h exerciseHit
		if err := rows.Scan(&h.ID, &h.Slug, &h.Name, &h.Kind, &h.Status, &h.Exact); err != nil {
			return nil, httpx.FromDB(err)
		}
		hits = append(hits, h)
	}
	return hits, httpx.FromDB(rows.Err())
}

// runExerciseSearch matches canonical names and aliases. The query is normalised the same way as
// exercise_aliases.alias_normalized. Official exercises and the caller's own are visible; merged and deleted are not.
func (s *Server) runExerciseSearch(ctx context.Context, userID uuid.UUID, q string, limit int64) ([]exerciseHit, error) {
	return s.exercisesScan(ctx, `with q as (
               select btrim(lower(regexp_replace($1::text, '[^[:alnum:]]+', ' ', 'g'))) as nq
           ), cand as (
               select e.id, e.slug, e.name, e.kind, e.status,
                      btrim(lower(regexp_replace(e.name, '[^[:alnum:]]+', ' ', 'g'))) as nname
                 from exercises e
                where e.deleted_at is null
                  and e.merged_into_id is null
                  and (e.status = 'official' or e.created_by = $2)
           ), scored as (
               select c.*,
                      greatest(
                          similarity(c.nname, q.nq),
                          coalesce((select max(similarity(a.alias_normalized, q.nq))
                                      from exercise_aliases a where a.exercise_id = c.id), 0)
                      ) as score,
                      (c.nname like '%' || q.nq || '%'
                       or exists (select 1 from exercise_aliases a
                                   where a.exercise_id = c.id
                                     and a.alias_normalized like '%' || q.nq || '%')) as contains,
                      (c.nname = q.nq
                       or exists (select 1 from exercise_aliases a
                                   where a.exercise_id = c.id
                                     and a.alias_normalized = q.nq)) as exact
                 from cand c, q
           )
           select id, slug, name, kind::text, status::text, exact
             from scored, q
            where q.nq = '' or contains or score > 0.3
            order by exact desc,
                     (case when q.nq = '' then 0 when contains then 1 else 0 end) desc,
                     score desc, name
            limit $3`, q, userID, limit)
}

// exercisesSuggesting: when a query is only tag words ("preacher"), list the exercises that suggest those tags.
func (s *Server) exercisesSuggesting(ctx context.Context, userID uuid.UUID, tagIDs []uuid.UUID, limit int64) ([]exerciseHit, error) {
	return s.exercisesScan(ctx, `select distinct e.id, e.slug, e.name,
                  e.kind::text, e.status::text,
                  false as exact
             from exercises e
             join exercise_variation_tags evt on evt.exercise_id = e.id
            where evt.tag_id = any($1::uuid[])
              and e.deleted_at is null and e.merged_into_id is null
              and (e.status = 'official' or e.created_by = $2)
            order by e.name
            limit $3`, tagIDs, userID, limit)
}

// searchExercises: if the whole query is not already an exact name or alias, tag words are stripped out and
// searched separately, so "preacher curl" finds Bicep Curl with `preacher` suggested.
func (s *Server) searchExercises(w http.ResponseWriter, r *http.Request, userID uuid.UUID) error {
	limit, ok := exercisesLimit(w, r)
	if !ok {
		return nil
	}
	q := r.URL.Query().Get("q")
	ctx := r.Context()
	full, err := s.runExerciseSearch(ctx, userID, q, limit)
	if err != nil {
		return err
	}
	plain := func(hits []exerciseHit) []exerciseOut {
		out := make([]exerciseOut, 0, len(hits))
		for _, h := range hits {
			out = append(out, h.out(nil))
		}
		return out
	}
	reply := func(out []exerciseOut) error {
		httpx.WriteJSON(w, http.StatusOK, out)
		return nil
	}

	queryTokens := exercisesTokens(q)
	if len(queryTokens) == 0 || slices.ContainsFunc(full, func(h exerciseHit) bool { return h.Exact }) {
		return reply(plain(full))
	}

	trows, err := s.DB.Query(ctx, `select id, name from variation_tags`)
	if err != nil {
		return httpx.FromDB(err)
	}
	var tags []exercisesPhrase
	for trows.Next() {
		var id uuid.UUID
		var name string
		if err := trows.Scan(&id, &name); err != nil {
			trows.Close()
			return httpx.FromDB(err)
		}
		tags = append(tags, exercisesPhrase{id, exercisesTokens(name)})
	}
	trows.Close()
	if err := trows.Err(); err != nil {
		return httpx.FromDB(err)
	}
	found, rest := exercisesExtractTags(queryTokens, tags)
	if len(found) == 0 {
		return reply(plain(full))
	}

	onlyTagWords := len(rest) == 0
	var stripped []exerciseHit
	if onlyTagWords {
		stripped, err = s.exercisesSuggesting(ctx, userID, found, limit)
	} else {
		stripped, err = s.runExerciseSearch(ctx, userID, strings.Join(rest, " "), limit)
	}
	if err != nil {
		return err
	}

	// A tag is only suggested where it makes sense: on an exact match of the remaining words, or where the
	// catalog lists it for that exercise.
	ids := make([]uuid.UUID, 0, len(stripped))
	for _, h := range stripped {
		ids = append(ids, h.ID)
	}
	type link struct{ ex, tag uuid.UUID }
	linked := map[link]bool{}
	lrows, err := s.DB.Query(ctx, `select exercise_id, tag_id from exercise_variation_tags
          where exercise_id = any($1::uuid[]) and tag_id = any($2::uuid[])`, ids, found)
	if err != nil {
		return httpx.FromDB(err)
	}
	for lrows.Next() {
		var l link
		if err := lrows.Scan(&l.ex, &l.tag); err != nil {
			lrows.Close()
			return httpx.FromDB(err)
		}
		linked[l] = true
	}
	lrows.Close()
	if err := lrows.Err(); err != nil {
		return httpx.FromDB(err)
	}

	byID := make(map[uuid.UUID]exerciseOut, len(stripped))
	strippedOut := make([]exerciseOut, 0, len(stripped))
	for _, h := range stripped {
		sug := []uuid.UUID{}
		for _, t := range found {
			if h.Exact || linked[link{h.ID, t}] {
				sug = append(sug, t)
			}
		}
		o := h.out(sug)
		strippedOut = append(strippedOut, o)
		byID[h.ID] = o
	}

	out := []exerciseOut{}
	if onlyTagWords {
		// The user may be mid-way through an exercise name ("overhead"), so name matches lead, carrying the
		// suggestion if the catalog lists the tag for them.
		for _, h := range full {
			var tags []uuid.UUID
			if o, ok := byID[h.ID]; ok {
				tags = o.SuggestedTagIDs
				delete(byID, h.ID)
			}
			out = append(out, h.out(tags))
		}
		tagOnly := make([]exerciseOut, 0, len(byID))
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
				out = append(out, h.out(nil))
			}
		}
	}
	if int64(len(out)) > limit {
		out = out[:limit]
	}
	return reply(out)
}

type tagOut struct {
	ID       uuid.UUID `json:"id"`
	Slug     string    `json:"slug"`
	Name     string    `json:"name"`
	Category string    `json:"category"`
}

func (s *Server) listTags(w http.ResponseWriter, r *http.Request, _ uuid.UUID) error {
	rows, err := s.DB.Query(r.Context(),
		`select id, slug, name, category::text from variation_tags order by variation_tags.category, name`)
	if err != nil {
		return httpx.FromDB(err)
	}
	defer rows.Close()
	out := []tagOut{}
	for rows.Next() {
		var t tagOut
		if err := rows.Scan(&t.ID, &t.Slug, &t.Name, &t.Category); err != nil {
			return httpx.FromDB(err)
		}
		out = append(out, t)
	}
	if err := rows.Err(); err != nil {
		return httpx.FromDB(err)
	}
	httpx.WriteJSON(w, http.StatusOK, out)
	return nil
}
