package httpapi

import "github.com/smarniw/metamorphosis/internal/db"

// accountAPI holds the handlers of the account operations in api/openapi.yaml.
type accountAPI struct{}

func newAccountAPI(_ Deps, _ *db.Queries) accountAPI { return accountAPI{} }
