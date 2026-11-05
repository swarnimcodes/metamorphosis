// Package httpx holds the API's error type and JSON helpers: every failure is `{ "error": ... }`.
package httpx

import (
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
)

// Error is a failure with a status the client sees. Internal errors keep their cause for the log only.
type Error struct {
	Status  int
	Message string
	// Code is an optional machine-readable value the client can branch on.
	Code string
	// Extra fields are merged into the top level of the body next to `error`.
	Extra map[string]any
	Err   error
}

func (e *Error) Error() string { return e.Message }
func (e *Error) Unwrap() error { return e.Err }

func NotFound() *Error           { return &Error{Status: http.StatusNotFound, Message: "not found"} }
func Unauthorized() *Error       { return &Error{Status: http.StatusUnauthorized, Message: "unauthorized"} }
func BadRequest(m string) *Error { return &Error{Status: http.StatusBadRequest, Message: m} }
func Conflict(m string) *Error   { return &Error{Status: http.StatusConflict, Message: m} }
func Upstream(m string) *Error   { return &Error{Status: http.StatusBadGateway, Message: m} }
func Internal(err error) *Error {
	return &Error{Status: http.StatusInternalServerError, Message: "internal server error", Err: err}
}
func Coded(status int, code, m string) *Error {
	return &Error{Status: status, Code: code, Message: m}
}
func ConflictWith(m string, extra map[string]any) *Error {
	return &Error{Status: http.StatusConflict, Message: m, Extra: extra}
}

// FromDB maps a database error the way the Rust API did: no rows is 404, constraint violations are the
// client's mistake, everything else is internal.
func FromDB(err error) error {
	if err == nil {
		return nil
	}
	var existing *Error
	if errors.As(err, &existing) {
		return err
	}
	if errors.Is(err, pgx.ErrNoRows) {
		return NotFound()
	}
	var pg *pgconn.PgError
	if errors.As(err, &pg) {
		c := pg.ConstraintName
		if c == "" {
			c = "unknown"
		}
		switch pg.Code {
		case "23505":
			return Conflict(fmt.Sprintf("already exists (%s)", c))
		case "23503":
			return BadRequest(fmt.Sprintf("referenced record does not exist (%s)", c))
		case "23514":
			return BadRequest(fmt.Sprintf("invalid value (%s)", c))
		}
	}
	return Internal(err)
}

// Constraint returns the violated unique constraint's name, or "".
func UniqueViolation(err error) string {
	var pg *pgconn.PgError
	if errors.As(err, &pg) && pg.Code == "23505" {
		return pg.ConstraintName
	}
	return ""
}

func WriteJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	if err := json.NewEncoder(w).Encode(v); err != nil {
		slog.Warn("writing response failed", "error", err)
	}
}

func writeError(w http.ResponseWriter, err error) {
	var e *Error
	if !errors.As(err, &e) {
		e = Internal(err)
	}
	if e.Status >= 500 && e.Err != nil {
		slog.Error("internal error", "error", e.Err)
	}
	body := map[string]any{"error": e.Message}
	if e.Code != "" {
		body["code"] = e.Code
	}
	for k, v := range e.Extra {
		body[k] = v
	}
	WriteJSON(w, e.Status, body)
}

type HandlerFunc func(w http.ResponseWriter, r *http.Request) error

// Handle turns a handler that returns an error into an http.HandlerFunc that renders it.
func Handle(h HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if err := h(w, r); err != nil {
			writeError(w, err)
		}
	}
}

// Decode reads a JSON body. A malformed one is a 400 in the API's usual shape.
func Decode(r *http.Request, v any) error {
	r.Body = http.MaxBytesReader(nil, r.Body, 2<<20)
	if err := json.NewDecoder(r.Body).Decode(v); err != nil {
		return BadRequest("invalid JSON body: " + err.Error())
	}
	return nil
}

func NoContent(w http.ResponseWriter) { w.WriteHeader(http.StatusNoContent) }
