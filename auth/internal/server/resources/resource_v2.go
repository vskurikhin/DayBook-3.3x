package resources

import (
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
	done := make(chan struct{})
	go func() {
		if v.cfg.Values().Debug {
			_, _ = w.Write([]byte("DEBUG "))
		}
		_, _ = w.Write([]byte("V2"))
		close(done)
	}()
	select {
	case <-done:
		return
	case <-ctx.Done():
	}
}

// NewV2 creates and returns a new V2 resource instance that implements
// the ResourceV2 interface.
func NewV2(cfg config.Config) *V2 {
	return &V2{cfg: cfg}
}
