package server

import (
	"context"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"

	"github.com/vskurikhin/DayBook-3.3x/auth/v2/internal/server/config"
)

const (
	ExitCodeStartHTTPServerError = 4
)

//go:generate mockgen -destination=../../mocks/server/mocks/server.go -package=mocks github.com/vskurikhin/DayBook-3.3x/auth/v2/internal/server Server
type Server interface {
	Run(ctx context.Context)
}

var _ Server = (*AuthServer)(nil)

type AuthServer struct {
	handler http.Handler
	values  config.Values
}

func NewAuthServer(cfg config.Config, handler http.Handler) *AuthServer {
	return &AuthServer{handler: handler, values: cfg.Values()}
}

func (a AuthServer) Run(ctx context.Context) {
	server := &http.Server{
		Addr:    a.values.Address,
		Handler: a.handler,
	}
	var err error
	// канал для перенаправления прерываний
	// поскольку нужно отловить всего одно прерывание,
	// ёмкости 1 для канала будет достаточно
	sigQuit := make(chan os.Signal, 1)
	// регистрируем перенаправление прерываний
	signal.Notify(sigQuit, syscall.SIGINT, syscall.SIGQUIT, syscall.SIGTERM)
	go func() {
		// Start the HTTP server on port 8080 by default
		fmt.Printf("HTTPServer starting on %s...\n", a.values.Address)
		err = server.ListenAndServe()
		close(sigQuit)
	}()
	for {
		select {
		case <-ctx.Done():
			slog.Info("ctx.Done", "msg", ctx.Err())
			_ = server.Shutdown(ctx)
			return
		case <-sigQuit:
			if err != nil {
				slog.Error("HTTPServer Error", "err", err)
				os.Exit(ExitCodeStartHTTPServerError)
			}
			slog.Info("HTTPServer Done")
			return
		}
	}
}
