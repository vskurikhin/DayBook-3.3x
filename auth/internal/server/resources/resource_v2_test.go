package resources

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func TestNewV2(t *testing.T) {
	v := NewV2(newTestConfig())

	if v == nil {
		t.Fatal("expected V2 instance, got nil")
	}
}

func TestV2_Ok_ReturnsV2(t *testing.T) {
	v := V2{cfg: newTestConfig()}

	req := httptest.NewRequest(http.MethodGet, "/ok", nil)
	rec := httptest.NewRecorder()

	v.Ok(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected status 200, got %d", rec.Code)
	}

	if rec.Body.String() != "V2" {
		t.Fatalf("expected body 'V2', got %s", rec.Body.String())
	}
}

func TestV2_Ok_ContextCanceled(t *testing.T) {
	v := V2{cfg: newTestConfig()}

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	req := httptest.NewRequest(http.MethodGet, "/ok", nil).WithContext(ctx)
	rec := httptest.NewRecorder()

	v.Ok(rec, req)

	body := rec.Body.String()

	if body != "" && body != "V2" {
		t.Fatalf("unexpected body: %s", body)
	}
}

func TestV2_Ok_ContextTimeout(t *testing.T) {
	v := V2{cfg: newTestConfig()}

	ctx, cancel := context.WithTimeout(context.Background(), time.Nanosecond)
	defer cancel()

	time.Sleep(time.Millisecond)

	req := httptest.NewRequest(http.MethodGet, "/ok", nil).WithContext(ctx)
	rec := httptest.NewRecorder()

	v.Ok(rec, req)

	body := rec.Body.String()

	if body != "" && body != "V2" {
		t.Fatalf("unexpected body: %s", body)
	}
}
