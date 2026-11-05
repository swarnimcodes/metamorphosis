package api

import (
	"bytes"
	"encoding/json"
	"errors"
	"math"
	"math/big"
	"net/http"
	"regexp"
	"strconv"
	"time"

	"github.com/google/uuid"

	"github.com/smarniw/metamorphosis/internal/httpx"
)

// A daily weigh-in log: one entry per user per local calendar day. The client sends the date in the user's
// own timezone, so there is no timezone conversion here; a day cannot be later than the latest local date
// anywhere (UTC+14).

func (s *Server) bodyweightsRoutes(mux *http.ServeMux) {
	h := httpx.Handle
	mux.HandleFunc("GET /body-weights", h(s.authed(s.bodyweightList)))
	mux.HandleFunc("PUT /body-weights/{date}", h(s.authed(s.bodyweightUpsert)))
	mux.HandleFunc("DELETE /body-weights/{date}", h(s.authed(s.bodyweightRemove)))
}

var bodyweightDateRe = regexp.MustCompile(`^([+-]?)([0-9]{4})-([0-9]{2})-([0-9]{2})$`)

// bodyweightParseDate accepts what time's "[year]-[month]-[day]" did: a 4-digit year with an optional sign
// (astronomical numbering, so year 0 exists), a 2-digit month and a 2-digit day, and a real calendar date.
func bodyweightParseDate(s string) (time.Time, error) {
	if m := bodyweightDateRe.FindStringSubmatch(s); m != nil {
		y, _ := strconv.Atoi(m[2])
		if m[1] == "-" {
			y = -y
		}
		mo, _ := strconv.Atoi(m[3])
		d, _ := strconv.Atoi(m[4])
		t := time.Date(y, time.Month(mo), d, 0, 0, 0, 0, time.UTC)
		if mo >= 1 && mo <= 12 && t.Year() == y && int(t.Month()) == mo && t.Day() == d {
			return t, nil
		}
	}
	return time.Time{}, httpx.BadRequest("date must be YYYY-MM-DD")
}

type bodyweightEntry struct {
	Date     string     `json:"date"`
	WeightKg statsFloat `json:"weight_kg"`
}

// maxBodyweightEntries is the most entries one request returns. Past it the oldest are dropped, never the newest.
const maxBodyweightEntries = 5000

// bodyweightList is oldest first, which is the order a chart wants.
func (s *Server) bodyweightList(w http.ResponseWriter, r *http.Request, userID uuid.UUID) error {
	q, err := statsQuery(r, "from", "to")
	if err != nil {
		return err
	}
	parse := func(key string) (*time.Time, error) {
		vs, ok := q[key]
		if !ok {
			return nil, nil
		}
		t, err := bodyweightParseDate(vs[0])
		if err != nil {
			return nil, err
		}
		return &t, nil
	}
	from, err := parse("from")
	if err != nil {
		return err
	}
	to, err := parse("to")
	if err != nil {
		return err
	}
	rows, err := s.DB.Query(r.Context(),
		`select "date", "weight_kg" from (
		       select to_char(measured_on, 'YYYY-MM-DD') as "date", weight_kg::float8 as "weight_kg", measured_on
		         from body_weights
		        where user_id = $1
		          and ($2::date is null or measured_on >= $2)
		          and ($3::date is null or measured_on <= $3)
		        order by measured_on desc
		        limit $4
		   ) newest
		   order by measured_on`, userID, from, to, int64(maxBodyweightEntries))
	if err != nil {
		return httpx.FromDB(err)
	}
	defer rows.Close()
	out := []bodyweightEntry{}
	for rows.Next() {
		var e bodyweightEntry
		var kg float64
		if err := rows.Scan(&e.Date, &kg); err != nil {
			return httpx.FromDB(err)
		}
		e.WeightKg = statsFloat(kg)
		out = append(out, e)
	}
	if err := rows.Err(); err != nil {
		return httpx.FromDB(err)
	}
	httpx.WriteJSON(w, http.StatusOK, out)
	return nil
}

// bodyweightDecimal is a rust_decimal::Decimal as serde read it: a JSON integer, a JSON float (taken through
// f64 and its shortest decimal representation) or a decimal string.
type bodyweightDecimal struct{ r *big.Rat }

func (d *bodyweightDecimal) UnmarshalJSON(b []byte) error {
	bad := errors.New("invalid value: expected a Decimal type representing a fixed-point number")
	b = bytes.TrimSpace(b)
	if len(b) == 0 || string(b) == "null" {
		return bad
	}
	if b[0] == '"' {
		var s string
		if err := json.Unmarshal(b, &s); err != nil {
			return err
		}
		r, ok := new(big.Rat).SetString(s)
		if !ok || bytes.ContainsAny([]byte(s), "/") {
			return bad
		}
		d.r = r
		return nil
	}
	if b[0] != '-' && (b[0] < '0' || b[0] > '9') {
		return bad
	}
	txt := string(b)
	if i, err := strconv.ParseInt(txt, 10, 64); err == nil {
		d.r = new(big.Rat).SetInt64(i)
		return nil
	}
	if u, err := strconv.ParseUint(txt, 10, 64); err == nil {
		d.r = new(big.Rat).SetUint64(u)
		return nil
	}
	f, err := strconv.ParseFloat(txt, 64)
	if err != nil || math.IsInf(f, 0) || math.IsNaN(f) {
		return bad
	}
	r, ok := new(big.Rat).SetString(strconv.FormatFloat(f, 'f', -1, 64))
	if !ok {
		return bad
	}
	d.r = r
	return nil
}

// bodyweightRound2 rounds half to even at 2 decimal places (rust_decimal's round_dp), returned as hundredths.
func bodyweightRound2(r *big.Rat) *big.Int {
	n := new(big.Int).Mul(r.Num(), big.NewInt(100))
	q, rem := new(big.Int).DivMod(n, r.Denom(), new(big.Int)) // denominator > 0: floor division
	twice := new(big.Int).Lsh(rem, 1)
	switch c := twice.Cmp(r.Denom()); {
	case c > 0, c == 0 && q.Bit(0) == 1:
		q.Add(q, big.NewInt(1))
	}
	return q
}

func (s *Server) bodyweightUpsert(w http.ResponseWriter, r *http.Request, userID uuid.UUID) error {
	// The body is read before the path date is checked, as the Rust extractors ran in that order.
	var body struct {
		WeightKg *bodyweightDecimal `json:"weight_kg"`
	}
	if err := httpx.Decode(r, &body); err != nil {
		return err
	}
	if body.WeightKg == nil {
		return httpx.BadRequest("Failed to deserialize the JSON body into the target type: missing field `weight_kg`")
	}
	day, err := bodyweightParseDate(r.PathValue("date"))
	if err != nil {
		return err
	}
	hundredths := bodyweightRound2(body.WeightKg.r)
	if hundredths.Sign() <= 0 || hundredths.Cmp(big.NewInt(50000)) >= 0 {
		return httpx.BadRequest("weight_kg must be between 0 and 500")
	}
	weight := new(big.Rat).SetFrac(hundredths, big.NewInt(100)).FloatString(2)

	// The date is the person's own local date, from wherever their device is. The furthest ahead any local
	// date can be is UTC+14, so that is the limit: a traveller already on tomorrow's date is fine, and only
	// a date that cannot exist anywhere yet is refused.
	ctx := r.Context()
	var latest time.Time
	if err := s.DB.QueryRow(ctx, `select (now() at time zone 'Etc/GMT-14')::date`).Scan(&latest); err != nil {
		return httpx.FromDB(err)
	}
	if day.After(latest) {
		return httpx.BadRequest("date cannot be in the future")
	}

	var e bodyweightEntry
	var kg float64
	if err := s.DB.QueryRow(ctx,
		`insert into body_weights (user_id, measured_on, weight_kg)
		 values ($1, $2, $3::text::numeric)
		 on conflict (user_id, measured_on) do update set weight_kg = excluded.weight_kg
		 returning to_char(measured_on, 'YYYY-MM-DD'), weight_kg::float8`, userID, day, weight).Scan(&e.Date, &kg); err != nil {
		return httpx.FromDB(err)
	}
	e.WeightKg = statsFloat(kg)
	httpx.WriteJSON(w, http.StatusOK, e)
	return nil
}

func (s *Server) bodyweightRemove(w http.ResponseWriter, r *http.Request, userID uuid.UUID) error {
	day, err := bodyweightParseDate(r.PathValue("date"))
	if err != nil {
		return err
	}
	var id uuid.UUID
	if err := s.DB.QueryRow(r.Context(),
		`delete from body_weights where user_id = $1 and measured_on = $2 returning id`, userID, day).Scan(&id); err != nil {
		return httpx.FromDB(err) // no row: 404
	}
	httpx.NoContent(w)
	return nil
}
