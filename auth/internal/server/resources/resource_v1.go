package resources

import (
	"bytes"
	"net/http"

	"github.com/vskurikhin/DayBook-3.3x/auth/v2/internal/server/config"
)

type ResourceV1 interface {
	Ok(w http.ResponseWriter, r *http.Request)
}

var _ ResourceV1 = (*V1)(nil)

type V1 struct {
	cfg config.Config
}

func (v V1) Ok(w http.ResponseWriter, _ *http.Request) {
	var body bytes.Buffer
	if v.cfg.Values().Debug {
		body.WriteString("DEBUG ")
	}
	body.WriteString("V1")
	//goland:noinspection ALL
	w.Write(body.Bytes())
}

// NewV1 creates and returns a new V1 resource instance that implements
// the ResourceV1 interface.
func NewV1(cfg config.Config) *V1 {
	return &V1{cfg: cfg}
}
