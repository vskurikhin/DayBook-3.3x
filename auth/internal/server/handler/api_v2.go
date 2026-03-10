package handler

import (
	"github.com/go-chi/chi/v5"

	"github.com/vskurikhin/DayBook-3.3x/auth/v2/internal/server/resources"
)

type ApiV2 interface {
	chi.Router
}

// NewApiV2 creates and configures a chi router for version 2 of the API.
// It registers HTTP routes that delegate request handling to the provided
// ResourceV2 implementation.
func NewApiV2(v2 resources.ResourceV2) ApiV2 {
	r := chi.NewRouter()
	r.Get(OK, v2.Ok)
	return r
}
