// Package handler provides HTTP routing and middleware configuration
// for the auth server.
//
// The package defines the HTTP layer of the application using the chi router.
// It is responsible for:
//
//   - Creating and configuring the HTTP router
//   - Registering global middleware (CORS, logging, recovery, timeout, etc.)
//   - Defining API endpoints
//
// The router is created via the NewRouter function, which accepts a
// config.Config instance. Configuration values (such as Debug mode)
// influence middleware behavior (for example, CORS debug logging).
//
// Middleware stack includes:
//
//   - CORS support (github.com/rs/cors)
//   - Request ID injection
//   - Real IP detection
//   - Structured logging
//   - Panic recovery
//   - Request timeout handling
package handler

import (
	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"
	"github.com/rs/cors"
	"net/http"

	"github.com/vskurikhin/DayBook-3.3x/auth/v2/internal/server/config"
	"github.com/vskurikhin/DayBook-3.3x/auth/v2/internal/server/env"
)

const (
	BaseURL = "/api"
	V1      = "/v1"
	V2      = "/v2"
	OK      = "/ok"
)

// NewRouter creates and configures an HTTP router with
// middleware and application routes using the provided configuration.
func NewRouter(cfg config.Config, env env.Environments, handlers ApiHandlers) http.Handler {
	r := chi.NewRouter()

	debug := cfg.Values().Debug

	c := cors.New(cors.Options{
		AllowedOrigins: []string{"*"},                                       // All origins
		AllowedMethods: []string{"POST", "GET", "PUT", "DELETE", "OPTIONS"}, // Allowing only get, just an example
		Debug:          debug,
	})

	r.Use(c.Handler)
	r.Use(middleware.RequestID)
	r.Use(middleware.RealIP)
	r.Use(middleware.Logger)
	r.Use(middleware.Recoverer)

	// Set a timeout value on the request context (ctx), that will signal
	// through ctx.Done() that the request has timed out and further
	// processing should be stopped.
	r.Use(middleware.Timeout(env.Values().Timeout))

	r.Mount(BaseURL+V1, handlers.apiV1())
	r.Mount(BaseURL+V2, handlers.apiV2())

	return r
}
