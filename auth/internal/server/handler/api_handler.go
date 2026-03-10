package handler

import (
	"encoding/json"
	"log/slog"
	"net/http"

	"github.com/vskurikhin/DayBook-3.3x/auth/v2/internal/server/resources"
)

// APIHandler wraps handlers to provide consistent error handling
// This pattern reduces boilerplate in route handlers
type APIHandler func(w http.ResponseWriter, r *http.Request) error

// ServeHTTP implements http.Handler for APIHandler
// This allows APIHandler to be used as a standard handler
func (fn APIHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	done := make(chan struct{})
	go func() {
		defer close(done)
		w.Header().Set("Content-Type", "application/json")

		// Call the actual handler
		if err := fn(w, r); err != nil {
			// Log the error for debugging
			slog.Error("API", slog.String("error", err.Error()))

			// Send error response
			w.WriteHeader(http.StatusInternalServerError)
			encodeErr := json.NewEncoder(w).Encode(resources.APIResponse{
				Success: false,
				Error:   err.Error(),
			})
			if encodeErr != nil {
				slog.Error("API", slog.String("error", encodeErr.Error()))
			}
			return
		}
	}()
	select {
	case <-ctx.Done():
		return
	case <-done:
		return
	}
}
