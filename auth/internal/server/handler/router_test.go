package handler

import (
	"github.com/go-chi/chi/v5"
	"go.uber.org/mock/gomock"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/vskurikhin/DayBook-3.3x/auth/v2/internal/server/config"
	"github.com/vskurikhin/DayBook-3.3x/auth/v2/internal/server/env"
)

//
// ---- Mock Config ----
//

type mockConfig struct {
	values config.Values
}

func (m *mockConfig) Values() config.Values {
	return m.values
}

func newMockConfig(debug bool) config.Config {
	return &mockConfig{
		values: config.Values{
			Debug: debug,
		},
	}
}

//
// ---- Mock Environments ----
//

type mockEnv struct {
	values env.Values
}

func (m mockEnv) Values() env.Values {
	return m.values
}

var environments = mockEnv{
	values: env.Values{
		Timeout: 5 * time.Second,
	},
}

// Empty Router

var emptyRouter = func() chi.Router {
	r := chi.NewRouter()
	r.Get("/", func(w http.ResponseWriter, r *http.Request) {})
	return r
}()

//
// ---- Tests ----
//

func TestNewRouter_ReturnsHandler(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()
	mockApiHandlers := NewMockApiHandlers(ctrl)
	cfg := newMockConfig(false)

	mockApiHandlers.EXPECT().apiV1().Return(func() ApiV1 {
		return emptyRouter
	}()).Times(1)
	mockApiHandlers.EXPECT().apiV2().Return(func() ApiV2 {
		return emptyRouter
	}()).Times(1)
	router := NewRouter(cfg, environments, mockApiHandlers)

	if router == nil {
		t.Fatal("expected router, got nil")
	}
}

func TestNewRouter_HiEndpoint_Returns200(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()
	mockApiHandlers := NewMockApiHandlers(ctrl)
	cfg := newMockConfig(false)

	mockApiHandlers.EXPECT().apiV1().Return(func() ApiV1 {
		r := chi.NewRouter()
		r.Get(OK, func(w http.ResponseWriter, r *http.Request) {
			_, _ = w.Write([]byte("ok"))
		})
		return r
	}()).Times(1)
	mockApiHandlers.EXPECT().apiV2().Return(func() ApiV2 {
		return emptyRouter
	}()).Times(1)

	router := NewRouter(cfg, environments, mockApiHandlers)

	req := httptest.NewRequest(http.MethodGet, "/api/v1/ok", nil)
	rr := httptest.NewRecorder()

	router.ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("expected status 200, got %d", rr.Code)
	}

	if body := rr.Body.String(); body != "ok" {
		t.Fatalf("expected body 'ok', got '%s'", body)
	}
}

func TestNewRouter_HiEndpoint_MethodNotAllowed(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()
	mockApiHandlers := NewMockApiHandlers(ctrl)
	cfg := newMockConfig(false)

	mockApiHandlers.EXPECT().apiV1().Return(func() ApiV1 {
		r := chi.NewRouter()
		r.Get(OK, func(w http.ResponseWriter, r *http.Request) {
			_, _ = w.Write([]byte("ok"))
		})
		return r
	}()).Times(1)
	mockApiHandlers.EXPECT().apiV2().Return(func() ApiV2 {
		return emptyRouter
	}()).Times(1)
	router := NewRouter(cfg, environments, mockApiHandlers)

	req := httptest.NewRequest(http.MethodPost, "/api/v1/ok", nil)
	rr := httptest.NewRecorder()

	router.ServeHTTP(rr, req)

	if rr.Code != http.StatusMethodNotAllowed {
		t.Fatalf("expected 405, got %d", rr.Code)
	}
}

func TestNewRouter_NotFound(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()
	mockApiHandlers := NewMockApiHandlers(ctrl)
	cfg := newMockConfig(false)

	mockApiHandlers.EXPECT().apiV1().Return(func() ApiV1 {
		return emptyRouter
	}()).Times(1)
	mockApiHandlers.EXPECT().apiV2().Return(func() ApiV2 {
		return emptyRouter
	}()).Times(1)
	router := NewRouter(cfg, environments, mockApiHandlers)

	req := httptest.NewRequest(http.MethodGet, "/unknown", nil)
	rr := httptest.NewRecorder()

	router.ServeHTTP(rr, req)

	if rr.Code != http.StatusNotFound {
		t.Fatalf("expected 404, got %d", rr.Code)
	}
}

func TestNewRouter_CORSHeaders(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()
	mockApiHandlers := NewMockApiHandlers(ctrl)
	cfg := newMockConfig(false)

	mockApiHandlers.EXPECT().apiV1().Return(func() ApiV1 {
		r := chi.NewRouter()
		r.Get(OK, func(w http.ResponseWriter, r *http.Request) {
			_, _ = w.Write([]byte("ok"))
		})
		return r
	}()).Times(1)
	mockApiHandlers.EXPECT().apiV2().Return(func() ApiV2 {
		return emptyRouter
	}()).Times(1)
	router := NewRouter(cfg, environments, mockApiHandlers)

	req := httptest.NewRequest(http.MethodOptions, "/api/v1/ok", nil)
	req.Header.Set("Origin", "http://localhost")
	req.Header.Set("Access-Control-Request-Method", "GET")

	rr := httptest.NewRecorder()

	router.ServeHTTP(rr, req)

	if rr.Header().Get("Access-Control-Allow-Origin") == "" {
		t.Fatal("expected CORS header, got none")
	}
}
