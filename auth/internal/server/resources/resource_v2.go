package resources

import (
	"bytes"
	"net/http"

	"github.com/vskurikhin/DayBook-3.3x/auth/v2/internal/server/config"
)

type ResourceV2 interface {
	Ok(w http.ResponseWriter, r *http.Request)
}

var _ ResourceV2 = (*V2)(nil)

type V2 struct {
	cfg config.Config
}

func (v V2) Ok(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	var body bytes.Buffer
	if v.cfg.Values().Debug {
		body.WriteString("DEBUG ")
	}
	body.WriteString("V2")
	select {
	case <-ctx.Done():
		return
	}
	//goland:noinspection ALL
	w.Write(body.Bytes())
}

// NewV2 creates and returns a new V2 resource instance that implements
// the ResourceV2 interface.
func NewV2(cfg config.Config) *V2 {
	return &V2{cfg: cfg}
}
