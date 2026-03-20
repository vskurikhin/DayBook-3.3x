package resources

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"

	"github.com/vskurikhin/DayBook-3.3x/auth/v2/internal/server/dto"
	"github.com/vskurikhin/DayBook-3.3x/auth/v2/internal/server/services"
	"github.com/vskurikhin/DayBook-3.3x/auth/v2/pkg/tool"
)

//go:generate mockgen -destination=auth_service_v2_mock_test.go -package=resources github.com/vskurikhin/DayBook-3.3x/auth/v2/internal/server/resources AuthServiceV2
type AuthServiceV2 interface {
	AuthServiceV1
	Auth(ctx context.Context, login services.Login) (services.Credentials, error)
	List(ctx context.Context) ([]services.User, error)
	Logout(ctx context.Context) error
	Refresh(ctx context.Context, token string) (services.Credentials, error)
	Register(ctx context.Context, user services.CreateUser) (services.Credentials, error)
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
	service services.AuthServiceV2
}

// Auth route
// @Summary Auth Краткое содержание
// @Description Auth - Описание (v2)
// @ID ResourceV2-auth
// @Tags    auth
// @Accept  json
// @Produce json
// @Param   request body dto.Login true "Request of auth"
// @Success 200 {object} APIResponse{data=dto.Token} "успешно"
// @Failure 500 {object} APIResponse{error=string,success=bool} "ошибка сервера "success": false"
// @Failure 504
// @Router /v2/auth [post]
func (v V2) Auth(w http.ResponseWriter, r *http.Request) error {
	body := http.MaxBytesReader(w, r.Body, 1<<20) // TODO introduce config parameter issue #59
	decoder := json.NewDecoder(body)
	var login dto.Login
	errDecode := decoder.Decode(&login)
	if errDecode != nil {
		return errDecode
	}
	creds, err := v.service.Auth(r.Context(), services.LoginFromDto(login))
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

// List route
// @Summary List Краткое содержание
// @Description List - Описание (v2)
// @ID ResourceV2-list
// @Tags ok
// @Produce json
// @Success 200 {object} APIResponse{data=[]dto.User} "успешно"
// @Failure 500 {object} APIResponse{error=string,success=bool} "ошибка сервера "success": false"
// @Failure 504
// @Router /v2/list [get]
func (v V2) List(w http.ResponseWriter, r *http.Request) error {
	list, err := v.service.List(r.Context())
	if err != nil {
		return err
	}
	return json.NewEncoder(w).Encode(APIResponse{
		Success: true,
		Data:    tool.Map(list, services.User.ToDto),
	})
}

// Logout route
// @Summary Logout Краткое содержание
// @Description Logout - Описание (v2)
// @ID ResourceV2-logout
// @Tags ok
// @Produce json
// @Success 200 {object} APIResponse{data=nil} "успешно"
// @Failure 500 {object} APIResponse{error=string,success=bool} "ошибка сервера "success": false"
// @Failure 504
// @Router /v2/logout [post]
func (v V2) Logout(_ http.ResponseWriter, r *http.Request) error {
	return v.service.Logout(r.Context())
}

// Ok route
// @Summary Ok Краткое содержание
// @Description Ok - Описание (v2)
// @ID ResourceV2-ok
// @Tags ok
// @Produce json
// @Success 200 {object} APIResponse{data=[]map[string]string} "успешно"
// @Failure 500 {object} APIResponse{error=string,success=bool} "ошибка сервера "success": false"
// @Failure 504
// @Router /v2/ok [get]
func (v V2) Ok(w http.ResponseWriter, _ *http.Request) error {
	return json.NewEncoder(w).Encode(APIResponse{
		Success: true,
		Data: []map[string]string{
			{"msg": v.service.Ok()},
		},
	})
}

// Refresh route
// @Summary Register Краткое содержание
// @Description Register - Описание (v2)
// @ID ResourceV2-refresh
// @Tags    refresh
// @Accept  json
// @Produce json
// @Param   request body dto.Login true "Request of refresh"
// @Success 200 {object} APIResponse{data=dto.Token} "успешно"
// @Failure 500 {object} APIResponse{error=string,success=bool} "ошибка сервера "success": false"
// @Failure 504
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
	body := http.MaxBytesReader(w, r.Body, 1<<20) // TODO introduce config parameter issue #59
	decoder := json.NewDecoder(body)
	var login dto.Login
	errDecode := decoder.Decode(&login)
	if errDecode != nil {
		return errDecode
	}
	_ = login
	creds, err := v.service.Refresh(r.Context(), cookie.Value)
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

// Register route
// @Summary Register Краткое содержание
// @Description Register - Описание (v2)
// @ID ResourceV2-register
// @Tags    register
// @Accept  json
// @Produce json
// @Param   request body dto.CreateUser true "Request of register"
// @Success 200 {object} APIResponse{data=dto.Token} "успешно"
// @Failure 500 {object} APIResponse{error=string,success=bool} "ошибка сервера "success": false"
// @Failure 504
// @Router /v2/register [post]
func (v V2) Register(w http.ResponseWriter, r *http.Request) error {
	body := http.MaxBytesReader(w, r.Body, 1<<20) // TODO introduce config parameter issue #59
	decoder := json.NewDecoder(body)
	var u dto.CreateUser
	errDecode := decoder.Decode(&u)
	if errDecode != nil {
		return errDecode
	}
	creds, err := v.service.Register(r.Context(), services.CreateUserFromDto(u))
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
func NewV2(service services.AuthServiceV2) *V2 {
	return &V2{service: service}
}
