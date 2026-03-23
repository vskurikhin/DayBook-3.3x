package resources

import (
	"bytes"
	"net/http"

	"github.com/vskurikhin/DayBook-3.3x/auth/v2/internal/server/services"
)

//go:generate mockgen -destination=mock_ok_service_v1_test.go -package=resources github.com/vskurikhin/DayBook-3.3x/auth/v2/internal/server/resources OkServiceV1
type OkServiceV1 interface {
	Ok() string
}

type ResourceV1 interface {
	Ok(w http.ResponseWriter, r *http.Request)
}

var _ ResourceV1 = (*V1)(nil)

type V1 struct {
	service services.OkServiceV1
}

// Ok route
// @Deprecated
// @Summary Ok Краткое содержание
// @Description Ok - Описание (v1)
// @ID ResourceV1-ok
// @Tags ok
// @Produce plain
// @Success 200 {object} string "успешно!"
// @Router /v1/ok [get]
func (v V1) Ok(w http.ResponseWriter, _ *http.Request) {
	var body bytes.Buffer
	body.WriteString(v.service.Ok())
	//goland:noinspection ALL
	w.Write(body.Bytes())
}

// NewV1 creates and returns a new V1 resource instance that implements
// the ResourceV1 interface.
func NewV1(service services.OkServiceV1) *V1 {
	return &V1{service: service}
}
