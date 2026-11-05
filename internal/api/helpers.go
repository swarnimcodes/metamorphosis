package api

import (
	"net/http"

	"github.com/google/uuid"

	"github.com/smarniw/metamorphosis/internal/httpx"
)

// pathUUID reads a {name} path segment as a UUID. A malformed one is a 400, as axum's Path<Uuid> rejection was.
func pathUUID(r *http.Request, name string) (uuid.UUID, error) {
	id, err := uuid.Parse(r.PathValue(name))
	if err != nil {
		return uuid.Nil, httpx.BadRequest("invalid " + name)
	}
	return id, nil
}
