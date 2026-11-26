package httpapi

import (
	"context"

	"github.com/smarniw/metamorphosis/internal/admin"
	"github.com/smarniw/metamorphosis/internal/db"
	"github.com/smarniw/metamorphosis/internal/httpapi/gen"
)

// adminAPI holds the handlers of the admin operations in api/openapi.yaml.
type adminAPI struct{ admin *admin.Service }

func newAdminAPI(d Deps, q *db.Queries) adminAPI {
	return adminAPI{admin: admin.NewService(q, func(ctx context.Context, fn func(*db.Queries) error) error {
		return db.InTx(ctx, d.DB, fn)
	})}
}

func adminUserOut(u admin.User) gen.AdminUser {
	return gen.AdminUser{Id: u.ID, Username: u.Username, DisplayName: u.DisplayName, Role: gen.AdminUserRole(u.Role), MemberSince: u.JoinedAt}
}

func (a adminAPI) ListAdminUsers(ctx context.Context, req gen.ListAdminUsersRequestObject) (gen.ListAdminUsersResponseObject, error) {
	q, filter := "", ""
	if req.Params.Q != nil {
		q = *req.Params.Q
	}
	if req.Params.Filter != nil {
		filter = string(*req.Params.Filter)
	}
	users, err := a.admin.List(ctx, userFrom(ctx), q, filter)
	if err != nil {
		return nil, err
	}
	out := make(gen.ListAdminUsers200JSONResponse, 0, len(users))
	for _, u := range users {
		out = append(out, adminUserOut(u))
	}
	return out, nil
}

func (a adminAPI) SetUserRole(ctx context.Context, req gen.SetUserRoleRequestObject) (gen.SetUserRoleResponseObject, error) {
	u, err := a.admin.SetRole(ctx, userFrom(ctx), req.Id, string(req.Body.Role))
	if err != nil {
		return nil, err
	}
	return gen.SetUserRole200JSONResponse(adminUserOut(u)), nil
}
