package resources

import (
	"encoding/json"
	"net/http"

	"github.com/vskurikhin/DayBook-3.3x/auth/v2/internal/server/config"
)

type ResourceV2 interface {
	Ok(w http.ResponseWriter, r *http.Request) error
}

var _ ResourceV2 = (*V2)(nil)

type V2 struct {
	cfg config.Config
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

// NewV2 creates and returns a new V2 resource instance that implements
// the ResourceV2 interface.
func NewV2(cfg config.Config) *V2 {
	return &V2{cfg: cfg}
}
