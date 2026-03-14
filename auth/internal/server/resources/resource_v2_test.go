package resources

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestNewV2(t *testing.T) {
	v := NewV2(newTestConfig(), nil)

	if v == nil {
		t.Fatal("expected V2 instance, got nil")
	}
}

func TestV2_Ok_ReturnsV2(t *testing.T) {
	v := V2{cfg: newTestConfig()}

	req := httptest.NewRequest(http.MethodGet, "/ok", nil)
	rec := httptest.NewRecorder()

	err := v.Ok(rec, req)
	if err != nil {
		t.Fatal("expected no error, got ", err)
	}

	if rec.Code != http.StatusOK {
		t.Fatalf("expected status 200, got %d", rec.Code)
	}

	if rec.Body.String() != `{"success":true,"data":[{"id":"1","version":"V2"}]}`+"\n" {
		t.Fatalf("expected body '"+`{"success":true,"data":[{"id":"1","version":"V2"}]}`+"', got %s", rec.Body.String())
	}
}

func TestV2_Ok_Returns_DEBUG_V2(t *testing.T) {
	v := V2{cfg: newTestConfigDebug()}

	req := httptest.NewRequest(http.MethodGet, "/ok", nil)
	rec := httptest.NewRecorder()

	err := v.Ok(rec, req)
	if err != nil {
		t.Fatal("expected no error, got ", err)
	}

	if rec.Code != http.StatusOK {
		t.Fatalf("expected status 200, got %d", rec.Code)
	}

	if rec.Body.String() != `{"success":true,"data":[{"id":"1","version":"V2"},{"debug":"true","id":"2"}]}`+"\n" {
		t.Fatalf("expected body '"+`{"success":true,"data":[{"id":"1","version":"V2"},{"debug":"true","id":"2"}]}`+"', got %s", rec.Body.String())
	}
}
