package handler

import (
	"net/http"
	"testing"

	"github.com/go-chi/chi/v5"
)

func newTestRouter(response string) chi.Router {
	r := chi.NewRouter()
	r.Get("/test", func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(response))
	})
	return r
}

func TestNewHandlers_ReturnsCorrectRouters(t *testing.T) {
	v1 := newTestRouter("v1")
	v2 := newTestRouter("v2")

	h := NewHandlers(v1, v2)

	if h == nil {
		t.Fatal("expected handlers instance")
	}

	if h.apiV1() != v1 {
		t.Fatal("apiV1 router mismatch")
	}

	if h.apiV2() != v2 {
		t.Fatal("apiV2 router mismatch")
	}
}
