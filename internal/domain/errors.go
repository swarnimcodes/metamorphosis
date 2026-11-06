// Package domain holds what every feature shares and nothing HTTP-specific: the errors a service returns.
// The HTTP layer decides what each kind looks like on the wire.
package domain

import "fmt"

// Kind says what went wrong in terms a caller can act on.
type Kind int

const (
	// KindInvalid is a request the service refuses on its merits (400).
	KindInvalid Kind = iota + 1
	// KindUnauthorized is a missing or no-longer-valid identity (401).
	KindUnauthorized
	// KindNotFound is a record that does not exist, or that is not the caller's (404).
	KindNotFound
	// KindConflict is a request that clashes with existing state (409).
	KindConflict
	// KindUpstream is a dependency that could not be reached (502).
	KindUpstream
)

// Error is a failure a service reports on purpose. Anything else a service returns is an unexpected fault (500).
type Error struct {
	Kind    Kind
	Message string
	// Code is an optional stable value a client can branch on.
	Code string
	// Extra fields are added to the top level of the error body, next to the message.
	Extra map[string]any
	Err   error
}

func (e *Error) Error() string { return e.Message }
func (e *Error) Unwrap() error { return e.Err }

func Invalid(format string, args ...any) *Error {
	return &Error{Kind: KindInvalid, Message: fmt.Sprintf(format, args...)}
}

// InvalidCode is Invalid with a code the client can branch on.
func InvalidCode(code, message string) *Error {
	return &Error{Kind: KindInvalid, Code: code, Message: message}
}

func Unauthorized() *Error { return &Error{Kind: KindUnauthorized, Message: "unauthorized"} }
func NotFound() *Error     { return &Error{Kind: KindNotFound, Message: "not found"} }

func Conflict(code, message string) *Error {
	return &Error{Kind: KindConflict, Code: code, Message: message}
}

// ConflictWith is a conflict whose body carries extra top-level fields (for example the similar records).
func ConflictWith(message string, extra map[string]any) *Error {
	return &Error{Kind: KindConflict, Message: message, Extra: extra}
}

func Upstream(message string) *Error { return &Error{Kind: KindUpstream, Message: message} }
