package server

import (
	"context"
	"net/http"
	"testing"
	"time"

	"github.com/vskurikhin/DayBook-3.3x/auth/v2/internal/server/config"
)

// Тестируем создание нового AuthServer
func TestNewAuthServer(t *testing.T) {
	cfg := config.Values{
		Address: "127.0.0.1:0", // Порт 0 для случайного свободного порта
		Debug:   true,
	}
	handler := http.NewServeMux()

	srv := NewAuthServer(newTestConfig(), handler)

	if srv.values.Address != cfg.Address {
		t.Errorf("expected address %s, got %s", cfg.Address, srv.values.Address)
	}

	if srv.handler != handler {
		t.Errorf("expected handler %v, got %v", handler, srv.handler)
	}
}

func TestAuthServer_Run_ListenError_ExitsProcess(t *testing.T) {
	cfg := newTestConfig() // вызовет ошибку ListenAndServe
	handler := http.NewServeMux()
	srv := NewAuthServer(cfg, handler)

	ctx, cancel := context.WithTimeout(context.Background(), time.Millisecond*100)
	go func() {
		time.Sleep(time.Millisecond * 100)
		cancel()
	}()
	srv.Run(ctx)
}

// Тестируем интерфейс Server
func TestAuthServer_Interface(t *testing.T) {
	var s Server = &AuthServer{}
	_ = s
}

var _ config.Config = (*testValuesConfig)(nil)

type testValuesConfig struct {
	values config.Values
}

func (t testValuesConfig) Values() config.Values {
	return t.values
}

func newTestConfig() *testValuesConfig {
	return &testValuesConfig{values: config.Values{Address: "127.0.0.1:0", Debug: true}}
}
