package handler

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

// mockResourceV2 реализует resources.ResourceV2
type mockResourceV2 struct {
	called bool
}

func (m *mockResourceV2) Ok(w http.ResponseWriter, _ *http.Request) {
	m.called = true
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write([]byte("ok"))
}

func TestNewApiV2_OKRoute(t *testing.T) {
	mock := &mockResourceV2{}

	router := NewApiV2(mock)

	req := httptest.NewRequest(http.MethodGet, OK, nil)
	rec := httptest.NewRecorder()

	router.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected status 200, got %d", rec.Code)
	}

	if rec.Body.String() != "ok" {
		t.Fatalf("expected body 'ok', got %s", rec.Body.String())
	}

	if !mock.called {
		t.Fatal("expected Ok handler to be called")
	}
}
