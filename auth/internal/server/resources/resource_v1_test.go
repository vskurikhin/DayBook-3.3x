package resources

import (
	"github.com/vskurikhin/DayBook-3.3x/auth/v2/internal/server/config"
	"net/http"
	"net/http/httptest"
	"testing"
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

func newTestConfigDebug() *testValuesConfig {
	return &testValuesConfig{values: config.Values{Address: "127.0.0.1:0", Debug: true}}
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

func TestV1_Ok_Returns_DEBUG_V1(t *testing.T) {
	v := V1{cfg: newTestConfigDebug()}

	req := httptest.NewRequest(http.MethodGet, "/ok", nil)
	rec := httptest.NewRecorder()

	v.Ok(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected status 200, got %d", rec.Code)
	}

	if rec.Body.String() != "DEBUG V1" {
		t.Fatalf("expected body 'V1', got %s", rec.Body.String())
	}
}
