package resources

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/vskurikhin/DayBook-3.3x/auth/v2/internal/server/config"
)

var _ config.Config = (*testValuesConfig)(nil)

type testValuesConfig struct {
	values config.Values
}

func (t testValuesConfig) Values() config.Values {
	return t.values
}

func newTestConfig() *testValuesConfig {
	return &testValuesConfig{values: config.Values{Address: "127.0.0.1:0", Debug: false}}
}

func TestNewV1(t *testing.T) {
	v := NewV1(newTestConfig())

	if v == nil {
		t.Fatal("expected V1 instance, got nil")
	}
}

func TestV1_Ok_ReturnsV1(t *testing.T) {
	v := V1{cfg: newTestConfig()}

	req := httptest.NewRequest(http.MethodGet, "/ok", nil)
	rec := httptest.NewRecorder()

	v.Ok(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected status 200, got %d", rec.Code)
	}

	if rec.Body.String() != "V1" {
		t.Fatalf("expected body 'V1', got %s", rec.Body.String())
	}
}

func TestV1_Ok_ContextCanceled(t *testing.T) {
	v := V1{cfg: newTestConfig()}

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	req := httptest.NewRequest(http.MethodGet, "/ok", nil).WithContext(ctx)
	rec := httptest.NewRecorder()

	v.Ok(rec, req)

	// Возможны два варианта:
	// либо handler успел записать ответ,
	// либо ctx.Done() завершил select раньше.

	body := rec.Body.String()

	if body != "" && body != "V1" {
		t.Fatalf("unexpected body: %s", body)
	}
}

func TestV1_Ok_ContextTimeout(t *testing.T) {
	v := V1{cfg: newTestConfig()}

	ctx, cancel := context.WithTimeout(context.Background(), 1*time.Nanosecond)
	defer cancel()

	time.Sleep(time.Millisecond)

	req := httptest.NewRequest(http.MethodGet, "/ok", nil).WithContext(ctx)
	rec := httptest.NewRecorder()

	v.Ok(rec, req)

	body := rec.Body.String()

	if body != "" && body != "V1" {
		t.Fatalf("unexpected body: %s", body)
	}
}
