package resources

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"

	"github.com/vskurikhin/DayBook-3.3x/auth/v2/internal/server/config"
	"github.com/vskurikhin/DayBook-3.3x/auth/v2/internal/server/dto"
	"github.com/vskurikhin/DayBook-3.3x/auth/v2/internal/server/services"
	"github.com/vskurikhin/DayBook-3.3x/auth/v2/internal/server/services/model"
	"github.com/vskurikhin/DayBook-3.3x/auth/v2/pkg/tool"
)

//go:generate mockgen -destination=mock_auth_service_v2_test.go -package=resources github.com/vskurikhin/DayBook-3.3x/auth/v2/internal/server/resources AuthServiceV2
type AuthServiceV2 interface {
	Auth(ctx context.Context, login model.Login) (model.Credentials, error)
}

//go:generate mockgen -destination=mock_config_test.go -package=resources github.com/vskurikhin/DayBook-3.3x/auth/v2/internal/server/resources Config
type Config interface {
	JWThs256SignKey(string)
	Values() config.Values
}

//go:generate mockgen -destination=mock_list_service_v2_test.go -package=resources github.com/vskurikhin/DayBook-3.3x/auth/v2/internal/server/resources ListServiceV2
type ListServiceV2 interface {
	List(ctx context.Context) ([]model.User, error)
}

//go:generate mockgen -destination=mock_logout_service_v2_test.go -package=resources github.com/vskurikhin/DayBook-3.3x/auth/v2/internal/server/resources LogoutServiceV2
type LogoutServiceV2 interface {
	Logout(ctx context.Context) error
}

//go:generate mockgen -destination=mock_ok_service_v2_test.go -package=resources github.com/vskurikhin/DayBook-3.3x/auth/v2/internal/server/resources OkServiceV2
type OkServiceV2 interface {
	Ok() string
}

//go:generate mockgen -destination=mock_refresh_service_v2_test.go -package=resources github.com/vskurikhin/DayBook-3.3x/auth/v2/internal/server/resources RefreshServiceV2
type RefreshServiceV2 interface {
	Refresh(ctx context.Context, token string) (model.Credentials, error)
}

//go:generate mockgen -destination=mock_register_service_v2_test.go -package=resources github.com/vskurikhin/DayBook-3.3x/auth/v2/internal/server/resources RegisterServiceV2
type RegisterServiceV2 interface {
	Register(ctx context.Context, user model.CreateUser) (model.Credentials, error)
}

type ResourceV2 interface {
	Auth(w http.ResponseWriter, r *http.Request) error
	List(w http.ResponseWriter, r *http.Request) error
	Logout(w http.ResponseWriter, r *http.Request) error
	Ok(w http.ResponseWriter, r *http.Request) error
	Refresh(w http.ResponseWriter, r *http.Request) error
	Register(w http.ResponseWriter, r *http.Request) error
}

var _ ResourceV2 = (*V2)(nil)

type V2 struct {
	authService     services.AuthServiceV2
	cfg             config.Config
	listService     services.ListServiceV2
	logoutService   services.LogoutServiceV2
	okService       services.OkServiceV2
	refreshService  services.RefreshServiceV2
	registerService services.RegisterServiceV2
}

// Auth godoc
// @Summary Authenticate user
// @Description Authenticates user using login and password. Returns access token and sets refresh token cookie.
// @Tags auth
// @Accept json
// @Produce json
// @Param request body dto.Login true "User credentials"
// @Success 200 {object} APIResponse{data=dto.Token}
// @Failure 400 {object} APIResponse{error=string,success=bool} "bad request"
// @Failure 500 {object} APIResponse{error=string,success=bool} "server error 'success': false"
// @Failure 503 {object} APIResponse{error=string,success=bool} "service unavailable"
// @Failure 504 {object} APIResponse{error=string,success=bool} "gateway timeout"
// @Router /v2/auth [post]
func (v V2) Auth(w http.ResponseWriter, r *http.Request) error {
	body := http.MaxBytesReader(w, r.Body, int64(v.cfg.Values().RequestMaxBytes))
	decoder := json.NewDecoder(body)
	var login dto.Login
	errDecode := decoder.Decode(&login)
	if errDecode != nil {
		return errDecode
	}
	creds, err := v.authService.Auth(r.Context(), model.LoginFromDto(login))
	if err != nil {
		return err
	}
	cookie := creds.RefreshTokenCookie()
	// Set the cookie in the response writer
	http.SetCookie(w, &cookie)

	return json.NewEncoder(w).Encode(APIResponse{
		Success: true,
		Data:    creds.AccessToken().ToDto(),
	})
}

// List godoc
// @Summary Get users list
// @Description Returns list of users. Requires JWT authentication.
// @Tags users
// @Security BearerAuth
// @Produce json
// @Success 200 {object} APIResponse{data=[]dto.User}
// @Failure 401 {object} string "unauthorized"
// @Failure 500 {object} APIResponse{error=string,success=bool} "server error 'success': false"
// @Failure 503 {object} APIResponse{error=string,success=bool} "service unavailable"
// @Failure 504 {object} APIResponse{error=string,success=bool} "gateway timeout"
// @Router /v2/list [get]
func (v V2) List(w http.ResponseWriter, r *http.Request) error {
	list, err := v.listService.List(r.Context())
	if err != nil {
		return err
	}
	return json.NewEncoder(w).Encode(APIResponse{
		Success: true,
		Data:    tool.Map(list, model.User.ToDto),
	})
}

// Logout godoc
// @Summary Logout user
// @Description Invalidates user session. Requires JWT authentication.
// @Tags auth
// @Security BearerAuth
// @Produce json
// @Success 200 {object} APIResponse
// @Failure 401 {object} string "unauthorized"
// @Failure 500 {object} APIResponse{error=string,success=bool} "server error 'success': false"
// @Failure 503 {object} APIResponse{error=string,success=bool} "service unavailable"
// @Failure 504 {object} APIResponse{error=string,success=bool} "gateway timeout"
// @Router /v2/logout [post]
func (v V2) Logout(_ http.ResponseWriter, r *http.Request) error {
	return v.logoutService.Logout(r.Context())
}

// Ok godoc
// @Summary Health check
// @Description Returns service status.
// @Tags health
// @Produce json
// @Success 200 {object} APIResponse{data=[]map[string]string}
// @Router /v2/ok [get]
func (v V2) Ok(w http.ResponseWriter, _ *http.Request) error {
	return json.NewEncoder(w).Encode(APIResponse{
		Success: true,
		Data: []map[string]string{
			{"msg": v.okService.Ok()},
		},
	})
}

// Refresh godoc
// @Summary Refresh tokens
// @Description Refreshes access token using refresh token from cookie. Sets new refresh cookie.
// @Tags auth
// @Accept json
// @Produce json
// @Param request body dto.Login true "User credentials by cookie string"
// @Success 200 {object} APIResponse{data=dto.Token}
// @Success 206
// @Failure 400 {object} APIResponse{error=string,success=bool} "bad request"
// @Failure 401 {object} string "unauthorized"
// @Failure 500 {object} APIResponse{error=string,success=bool} "server error 'success': false"
// @Failure 503 {object} APIResponse{error=string,success=bool} "service unavailable"
// @Failure 504 {object} APIResponse{error=string,success=bool} "gateway timeout"
// @Router /v2/refresh [post]
func (v V2) Refresh(w http.ResponseWriter, r *http.Request) error {
	cookie, err := r.Cookie("refresh")
	if err != nil {
		slog.ErrorContext(r.Context(),
			"failed to parse cookie",
			slog.String("error", err.Error()),
			slog.String("errorType", fmt.Sprintf("%T", err)),
		)
		return err
	}
	body := http.MaxBytesReader(w, r.Body, int64(v.cfg.Values().RequestMaxBytes))
	decoder := json.NewDecoder(body)
	var login dto.Login
	errDecode := decoder.Decode(&login)
	if errDecode != nil {
		return errDecode
	}
	_ = login
	creds, err := v.refreshService.Refresh(r.Context(), cookie.Value)
	if err != nil {
		return err
	}
	refreshCookie := creds.RefreshTokenCookie()
	// Set the cookie in the response writer
	http.SetCookie(w, &refreshCookie)
	return json.NewEncoder(w).Encode(APIResponse{
		Success: true,
		Data:    creds.AccessToken().ToDto(),
	})
}

// Register godoc
// @Summary Register user
// @Description Registers a new user and returns access token with refresh cookie.
// @Tags auth
// @Accept json
// @Produce json
// @Param request body dto.CreateUser true "User registration data"
// @Success 200 {object} APIResponse{data=dto.Token}
// @Failure 400 {object} APIResponse{error=string,success=bool} "bad request"
// @Failure 409 {object} APIResponse{error=string,success=bool} "status conflict"
// @Failure 500 {object} APIResponse{error=string,success=bool} "server error 'success': false"
// @Failure 503 {object} APIResponse{error=string,success=bool} "service unavailable"
// @Failure 504 {object} APIResponse{error=string,success=bool} "gateway timeout"
// @Router /v2/register [post]
func (v V2) Register(w http.ResponseWriter, r *http.Request) error {
	body := http.MaxBytesReader(w, r.Body, int64(v.cfg.Values().RequestMaxBytes))
	decoder := json.NewDecoder(body)
	var u dto.CreateUser
	errDecode := decoder.Decode(&u)
	if errDecode != nil {
		return errDecode
	}
	creds, err := v.registerService.Register(r.Context(), model.CreateUserFromDto(u))
	if err != nil {
		return err
	}
	cookie := creds.RefreshTokenCookie()
	// Set the cookie in the response writer
	http.SetCookie(w, &cookie)
	return json.NewEncoder(w).Encode(APIResponse{
		Success: true,
		Data:    creds.AccessToken().ToDto(),
	})
}

// NewV2 creates and returns a new V2 resource instance that implements
// the ResourceV2 interface.
func NewV2(
	authService services.AuthServiceV2,
	cfg config.Config,
	listService services.ListServiceV2,
	logoutService services.LogoutServiceV2,
	okService services.OkServiceV2,
	refreshService services.RefreshServiceV2,
	registerService services.RegisterServiceV2,
) *V2 {
	return &V2{
		authService:     authService,
		cfg:             cfg,
		listService:     listService,
		logoutService:   logoutService,
		okService:       okService,
		refreshService:  refreshService,
		registerService: registerService,
	}
}
