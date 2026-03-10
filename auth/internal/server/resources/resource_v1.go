package resources

import (
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

func (v V1) Ok(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	done := make(chan struct{})
	go func() {
		if v.cfg.Values().Debug {
			_, _ = w.Write([]byte("DEBUG "))
		}
		_, _ = w.Write([]byte("V1"))
		close(done)
	}()
	select {
	case <-done:
		return
	case <-ctx.Done():
	}
}

// NewV1 creates and returns a new V1 resource instance that implements
// the ResourceV1 interface.
func NewV1(cfg config.Config) *V1 {
	return &V1{cfg: cfg}
}
