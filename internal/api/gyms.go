package api

import (
	"context"
	"github.com/go-chi/chi/v5"
	"net/http"
	"strings"
	"unicode"

	"github.com/google/uuid"

	"github.com/smarniw/metamorphosis/internal/httpx"
)

func (s *Server) gymsRoutes(r chi.Router) {
	h := httpx.Handle
	r.MethodFunc("GET", "/gyms", h(s.authed(s.searchGyms)))
	r.MethodFunc("POST", "/gyms", h(s.authed(s.createGym)))
}

type gymOut struct {
	ID       uuid.UUID   `json:"id"`
	Name     string      `json:"name"`
	Chain    *string     `json:"chain"`
	Area     *string     `json:"area"`
	Address  *string     `json:"address"`
	City     *string     `json:"city"`
	Lat      *statsFloat `json:"lat"`
	Lng      *statsFloat `json:"lng"`
	MapsURL  *string     `json:"maps_url"`
	Verified bool        `json:"verified"`
}

func (s *Server) gymsQuery(ctx context.Context, sql string, args ...any) ([]gymOut, error) {
	rows, err := s.DB.Query(ctx, sql, args...)
	if err != nil {
		return nil, httpx.FromDB(err)
	}
	defer rows.Close()
	out := []gymOut{}
	for rows.Next() {
		var g gymOut
		if err := rows.Scan(&g.ID, &g.Name, &g.Chain, &g.Area, &g.Address, &g.City, &g.Lat, &g.Lng, &g.MapsURL, &g.Verified); err != nil {
			return nil, httpx.FromDB(err)
		}
		out = append(out, g)
	}
	return out, httpx.FromDB(rows.Err())
}

// searchGyms: name search by trigram similarity or substring. Both sides are normalised so "gold's gym" finds
// "Golds Gym". Gyms the caller trained at most recently come first, then verified ones.
func (s *Server) searchGyms(w http.ResponseWriter, r *http.Request, userID uuid.UUID) error {
	limit, ok := exercisesLimit(w, r)
	if !ok {
		return nil
	}
	rows, err := s.gymsQuery(r.Context(), `with q as (
               select btrim(lower(regexp_replace(replace(replace($1::text, '''', ''), '’', ''),
                                                 '[^[:alnum:]]+', ' ', 'g'))) as nq
           ), scored as (
               select g.id, g.name, g.chain, g.area, g.address, g.city, g.lat, g.lng, g.maps_url, g.verified,
                      (select max(w.started_at) from workouts w
                        where w.gym_id = g.id and w.user_id = $2 and w.deleted_at is null) as last_used,
                      similarity(n.nname, q.nq) as score,
                      -- every word of the query appears in the name, area or city, in any order
                      not exists (
                          select 1 from unnest(string_to_array(q.nq, ' ')) as w
                           where w <> '' and n.hay not like '%' || w || '%'
                      ) as contains
                 from gyms g
                 cross join q
                 cross join lateral (
                     select btrim(lower(regexp_replace(replace(replace(g.name, '''', ''), '’', ''),
                                                       '[^[:alnum:]]+', ' ', 'g'))) as nname,
                            btrim(lower(regexp_replace(replace(replace(
                                g.name || ' ' || coalesce(g.area, '') || ' ' || coalesce(g.city, ''), '''', ''), '’', ''),
                                '[^[:alnum:]]+', ' ', 'g'))) as hay
                 ) n
                where g.deleted_at is null
           )
           select id, name, chain, area, address, city, lat, lng, maps_url, verified
             from scored, q
            where q.nq = '' or contains or score > 0.3
            order by (case when q.nq = '' then true else contains end) desc,
                     (case when q.nq = '' then 0 else score end) desc,
                     last_used desc nulls last, verified desc, name
            limit $3`, r.URL.Query().Get("q"), userID, limit)
	if err != nil {
		return err
	}
	httpx.WriteJSON(w, http.StatusOK, rows)
	return nil
}

type createGymRequest struct {
	Name    *string `json:"name"`
	Chain   *string `json:"chain"`
	Area    *string `json:"area"`
	Address *string `json:"address"`
	City    *string `json:"city"`
	MapsURL *string `json:"maps_url"`
	Force   bool    `json:"force"`
}

// gymsValidMapsURL: only links to a known maps service are kept: the directory is shared, so a stored link is
// opened by other people and must not be an arbitrary site.
func gymsValidMapsURL(url string) bool {
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

// gymsClean: blank optional strings are stored as null.
func gymsClean(v *string) *string {
	if v == nil {
		return nil
	}
	t := strings.TrimSpace(*v)
	if t == "" {
		return nil
	}
	return &t
}

// createGym refuses to create a near-duplicate unless `force` is set; the 409 lists the candidates.
func (s *Server) createGym(w http.ResponseWriter, r *http.Request, userID uuid.UUID) error {
	var body createGymRequest
	if err := httpx.Decode(r, &body); err != nil {
		return err
	}
	if body.Name == nil {
		return httpx.BadRequest("Failed to deserialize the JSON body into the target type: missing field `name`")
	}
	name := strings.TrimSpace(*body.Name)
	if name == "" {
		return httpx.BadRequest("name must not be empty")
	}
	chain, area, address, city := gymsClean(body.Chain), gymsClean(body.Area), gymsClean(body.Address), gymsClean(body.City)
	mapsURL := gymsClean(body.MapsURL)
	if mapsURL != nil && !gymsValidMapsURL(*mapsURL) {
		return httpx.BadRequest("maps_url must be a link to Google Maps, Apple Maps or OpenStreetMap")
	}
	ctx := r.Context()

	if !body.Force {
		similar, err := s.gymsQuery(ctx, `with q as (
                   select btrim(lower(regexp_replace(replace(replace($1::text, '''', ''), '’', ''),
                                                     '[^[:alnum:]]+', ' ', 'g'))) as nq
               ), cand as (
                   select g.id, g.name, g.chain, g.area, g.address, g.city, g.lat, g.lng, g.maps_url, g.verified,
                          similarity(btrim(lower(regexp_replace(replace(replace(g.name, '''', ''), '’', ''),
                                                                '[^[:alnum:]]+', ' ', 'g'))), q.nq) as score
                     from gyms g, q
                    where g.deleted_at is null
                      and ($2::text is null or lower(g.city) = lower($2))
               )
               select id, name, chain, area, address, city, lat, lng, maps_url,
                      verified
                 from cand
                where score > 0.5
                order by score desc, name
                limit 10`, name, city)
		if err != nil {
			return err
		}
		if len(similar) > 0 {
			return httpx.ConflictWith("similar gyms exist", map[string]any{"similar": similar})
		}
	}

	rows, err := s.gymsQuery(ctx, `insert into gyms (name, chain, area, address, city, maps_url, created_by)
         values ($1, $2, $3, $4, $5, $6, $7)
         returning id, name, chain, area, address, city, lat, lng, maps_url, verified`,
		name, chain, area, address, city, mapsURL, userID)
	if err != nil {
		return err
	}
	httpx.WriteJSON(w, http.StatusCreated, rows[0])
	return nil
}
