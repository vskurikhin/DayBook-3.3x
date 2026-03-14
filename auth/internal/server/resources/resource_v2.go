package resources

import (
	"encoding/json"
	"net/http"

	"github.com/vskurikhin/DayBook-3.3x/auth/v2/internal/server/config"
	"github.com/vskurikhin/DayBook-3.3x/auth/v2/internal/server/dto"
	"github.com/vskurikhin/DayBook-3.3x/auth/v2/internal/server/services"
)

//go:generate mockgen -destination=resource_v2_mock_test.go -package=resources github.com/vskurikhin/DayBook-3.3x/auth/v2/internal/server/resources ResourceV2
type ResourceV2 interface {
	Auth(w http.ResponseWriter, r *http.Request) error
	Ok(w http.ResponseWriter, r *http.Request) error
	Register(w http.ResponseWriter, r *http.Request) error
}

var _ ResourceV2 = (*V2)(nil)

type V2 struct {
	cfg     config.Config
	service services.BaseService
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
	users := []map[string]string{
		{"id": "1", "version": "V2"},
	}
	if v.cfg.Values().Debug {
		users = append(users, map[string]string{"id": "2", "debug": "true"})
	}
	return json.NewEncoder(w).Encode(APIResponse{
		Success: true,
		Data:    users,
	})
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
	decoder := json.NewDecoder(r.Body)
	var login dto.Login
	errDecode := decoder.Decode(&login)
	if errDecode != nil {
		return errDecode
	}
	token, err := v.service.Auth(r.Context(), services.UserFromDto(login))
	if err != nil {
		return err
	}
	return json.NewEncoder(w).Encode(APIResponse{
		Success: true,
		Data:    token.ToDto(),
	})
}

// Register route
// @Summary Register Краткое содержание
// @Description Register - Описание (v2)
// @ID ResourceV2-register
// @Tags    register
// @Accept  json
// @Produce json
// @Param   request body dto.Login true "Request of register"
// @Success 200 {object} APIResponse{data=dto.CreatedUser} "успешно"
// @Failure 500 {object} APIResponse{error=string,success=bool} "ошибка сервера "success": false"
// @Failure 504
// @Router /v2/register [post]
func (v V2) Register(w http.ResponseWriter, r *http.Request) error {
	decoder := json.NewDecoder(r.Body)
	var login dto.Login
	errDecode := decoder.Decode(&login)
	if errDecode != nil {
		return errDecode
	}
	user, err := v.service.Register(r.Context(), services.UserFromDto(login))
	if err != nil {
		return err
	}
	return json.NewEncoder(w).Encode(APIResponse{
		Success: true,
		Data:    user.ToDto(),
	})
}

// NewV2 creates and returns a new V2 resource instance that implements
// the ResourceV2 interface.
func NewV2(cfg config.Config, service services.BaseService) *V2 {
	return &V2{cfg: cfg, service: service}
}
