package handler

import (
	"github.com/go-chi/chi/v5"
	"github.com/vskurikhin/DayBook-3.3x/auth/v2/internal/server/config"
)

//go:generate mockgen -destination=config_mock_test.go -package=handler github.com/vskurikhin/DayBook-3.3x/auth/v2/internal/server/handler Config
type Config interface {
	JWThs256SignKey(string)
	Values() config.Values
}

//go:generate mockgen -destination=api_handlers_mock_test.go -package=handler github.com/vskurikhin/DayBook-3.3x/auth/v2/internal/server/handler ApiHandlers
type ApiHandlers interface {
	apiV1() chi.Router
	apiV2() chi.Router
}

var _ ApiHandlers = (*Handlers)(nil)

type Handlers struct {
	handlerV1 chi.Router
	handlerV2 chi.Router
}

func (h Handlers) apiV1() chi.Router {
	return h.handlerV1
}

func (h Handlers) apiV2() chi.Router {
	return h.handlerV2
}

// NewHandlers creates a Handlers instance that aggregates routers for API
// versions v1 and v2. The provided v1 and v2 routers are stored internally
// and later returned by the corresponding apiV1() and apiV2() methods.
func NewHandlers(v1 ApiV1, v2 ApiV2) *Handlers {
	return &Handlers{
		handlerV1: v1,
		handlerV2: v2,
	}
}
