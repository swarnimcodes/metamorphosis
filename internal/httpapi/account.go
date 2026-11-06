package httpapi

import (
	"context"

	"github.com/smarniw/metamorphosis/internal/account"
	"github.com/smarniw/metamorphosis/internal/db"
	"github.com/smarniw/metamorphosis/internal/domain"
	"github.com/smarniw/metamorphosis/internal/httpapi/gen"
)

// accountAPI holds the handlers of the account operations in api/openapi.yaml.
type accountAPI struct {
	svc *account.Service
	// google and devLogin say whether the optional sign-in routes exist; when they do not, the route answers 404.
	google, devLogin bool
}

func newAccountAPI(d Deps, q *db.Queries) accountAPI {
	var verifier account.GoogleVerifier
	if d.Google != nil {
		verifier = d.Google
	}
	inTx := func(ctx context.Context, fn func(account.Store) error) error {
		return db.InTx(ctx, d.DB, func(tx *db.Queries) error { return fn(tx) })
	}
	sessions := account.AuthSessions{Pool: d.DB, Cfg: d.Cfg}
	return accountAPI{
		svc:      account.New(q, inTx, sessions, verifier),
		google:   d.Google != nil,
		devLogin: d.Cfg.DevLogin,
	}
}

// userAgent is the caller's User-Agent, stored with the session; none when absent or empty.
func userAgent(ua *string) *string {
	if ua == nil || *ua == "" {
		return nil
	}
	return ua
}

func me(p account.Profile) gen.Me {
	return gen.Me{
		Id: p.ID, Username: p.Username, DisplayName: p.DisplayName, Email: p.Email,
		AvatarUrl: p.AvatarURL, UnitSystem: p.UnitSystem, Timezone: p.Timezone,
	}
}

func loginResponse(l account.Login) gen.LoginResponse {
	return gen.LoginResponse{
		AccessToken: l.Session.AccessToken, TokenType: "Bearer", ExpiresIn: l.Session.ExpiresIn,
		RefreshToken: l.Session.RefreshToken, RefreshExpiresIn: l.Session.RefreshExpiresIn,
		UserId: l.UserID, Username: l.Username,
	}
}

func (a accountAPI) GetMe(ctx context.Context, _ gen.GetMeRequestObject) (gen.GetMeResponseObject, error) {
	p, err := a.svc.Me(ctx, userFrom(ctx))
	if err != nil {
		return nil, err
	}
	return gen.GetMe200JSONResponse(me(p)), nil
}

func (a accountAPI) UpdateMe(ctx context.Context, req gen.UpdateMeRequestObject) (gen.UpdateMeResponseObject, error) {
	var c account.ProfileChanges
	if b := req.Body; b != nil {
		c = account.ProfileChanges{DisplayName: b.DisplayName, UnitSystem: b.UnitSystem, Timezone: b.Timezone}
	}
	p, err := a.svc.UpdateProfile(ctx, userFrom(ctx), c)
	if err != nil {
		return nil, err
	}
	return gen.UpdateMe200JSONResponse(me(p)), nil
}

func (a accountAPI) DeleteMe(ctx context.Context, req gen.DeleteMeRequestObject) (gen.DeleteMeResponseObject, error) {
	if req.Body == nil || req.Body.ConfirmUsername == nil {
		return nil, domain.Invalid("confirm_username is required")
	}
	if err := a.svc.Delete(ctx, userFrom(ctx), *req.Body.ConfirmUsername); err != nil {
		return nil, err
	}
	return gen.DeleteMe204Response{}, nil
}

func (a accountAPI) RefreshSession(ctx context.Context, req gen.RefreshSessionRequestObject) (gen.RefreshSessionResponseObject, error) {
	if req.Body == nil || req.Body.RefreshToken == nil {
		return nil, domain.Invalid("refresh_token is required")
	}
	l, err := a.svc.Refresh(ctx, *req.Body.RefreshToken, userAgent(req.Params.UserAgent))
	if err != nil {
		return nil, err
	}
	return gen.RefreshSession200JSONResponse(loginResponse(l)), nil
}

func (a accountAPI) DevLogin(ctx context.Context, req gen.DevLoginRequestObject) (gen.DevLoginResponseObject, error) {
	if !a.devLogin {
		return nil, domain.NotFound()
	}
	if req.Body == nil || req.Body.Username == nil {
		return nil, domain.Invalid("username is required")
	}
	l, err := a.svc.DevLogin(ctx, *req.Body.Username, req.Body.DisplayName, userAgent(req.Params.UserAgent))
	if err != nil {
		return nil, err
	}
	return gen.DevLogin200JSONResponse(loginResponse(l)), nil
}

func (a accountAPI) GoogleSignIn(ctx context.Context, req gen.GoogleSignInRequestObject) (gen.GoogleSignInResponseObject, error) {
	if !a.google {
		return nil, domain.NotFound()
	}
	if req.Body == nil || req.Body.IdToken == nil {
		return nil, domain.Invalid("id_token is required")
	}
	res, err := a.svc.GoogleSignIn(ctx, *req.Body.IdToken, userAgent(req.Params.UserAgent))
	if err != nil {
		return nil, err
	}
	var out gen.GoogleSignIn200JSONResponse
	if l := res.Login; l != nil {
		r := loginResponse(*l)
		err = out.FromGoogleLoggedIn(gen.GoogleLoggedIn{
			SignupRequired: false,
			AccessToken:    r.AccessToken, TokenType: r.TokenType, ExpiresIn: r.ExpiresIn,
			RefreshToken: r.RefreshToken, RefreshExpiresIn: r.RefreshExpiresIn, UserId: r.UserId, Username: r.Username,
		})
	} else {
		o := res.Signup
		err = out.FromGoogleSignupRequired(gen.GoogleSignupRequired{
			SignupRequired: true, SignupToken: o.Token, SuggestedUsername: o.SuggestedUsername,
			DisplayName: o.DisplayName, Email: o.Email, AvatarUrl: o.AvatarURL,
		})
	}
	if err != nil {
		return nil, err
	}
	return out, nil
}

func (a accountAPI) GoogleSignup(ctx context.Context, req gen.GoogleSignupRequestObject) (gen.GoogleSignupResponseObject, error) {
	if !a.google {
		return nil, domain.NotFound()
	}
	if req.Body == nil || req.Body.SignupToken == nil || req.Body.Username == nil {
		return nil, domain.Invalid("signup_token and username are required")
	}
	l, err := a.svc.GoogleSignup(ctx, *req.Body.SignupToken, *req.Body.Username, req.Body.DisplayName, userAgent(req.Params.UserAgent))
	if err != nil {
		return nil, err
	}
	return gen.GoogleSignup200JSONResponse(loginResponse(l)), nil
}

func (a accountAPI) UsernameAvailable(ctx context.Context, req gen.UsernameAvailableRequestObject) (gen.UsernameAvailableResponseObject, error) {
	if !a.google {
		return nil, domain.NotFound()
	}
	reason, err := a.svc.Availability(ctx, req.Params.Username)
	if err != nil {
		return nil, err
	}
	if reason == "" {
		return gen.UsernameAvailable200JSONResponse{Available: true}, nil
	}
	return gen.UsernameAvailable200JSONResponse{Available: false, Reason: &reason}, nil
}
