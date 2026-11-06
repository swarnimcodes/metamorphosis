package httpapi

import (
	"errors"
	"fmt"
	"net/http"
	"time"

	"github.com/smarniw/metamorphosis/internal/domain"
	"github.com/smarniw/metamorphosis/internal/httpapi/gen"
	"github.com/smarniw/metamorphosis/internal/httpx"
)

var statusOf = map[domain.Kind]int{
	domain.KindInvalid:      http.StatusBadRequest,
	domain.KindUnauthorized: http.StatusUnauthorized,
	domain.KindNotFound:     http.StatusNotFound,
	domain.KindConflict:     http.StatusConflict,
	domain.KindUpstream:     http.StatusBadGateway,
}

// toHTTP turns whatever a handler returned into the API's error: a service's domain error keeps its message and
// code, a database error becomes what it means to the client (a violated constraint is the client's mistake), and
// anything else is a 500 whose cause is logged, never sent.
func toHTTP(err error) error {
	var de *domain.Error
	if errors.As(err, &de) {
		return &httpx.Error{Status: statusOf[de.Kind], Message: de.Message, Code: de.Code, Extra: de.Extra, Err: de.Err}
	}
	return httpx.FromDB(err)
}

func renderError(w http.ResponseWriter, _ *http.Request, err error) {
	httpx.RenderError(w, toHTTP(err))
}

// badRequest answers a request the generated code could not even decode: malformed JSON, a wrong type, a value in
// a path or query parameter that is not what the spec says.
func badRequest(w http.ResponseWriter, _ *http.Request, err error) {
	var format *gen.InvalidParamFormatError
	if errors.As(err, &format) {
		// Every date in the spec is a calendar date, so a date that does not parse says what a date looks like.
		var notADate *time.ParseError
		if errors.As(format.Err, &notADate) {
			httpx.RenderError(w, httpx.BadRequest("date must be YYYY-MM-DD"))
			return
		}
		httpx.RenderError(w, httpx.BadRequest(fmt.Sprintf("invalid %s", format.ParamName)))
		return
	}
	var required *gen.RequiredParamError
	if errors.As(err, &required) {
		httpx.RenderError(w, httpx.BadRequest(fmt.Sprintf("%s is required", required.ParamName)))
		return
	}
	httpx.RenderError(w, httpx.BadRequest("invalid request: "+err.Error()))
}
