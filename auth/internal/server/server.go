package server

import (
	"context"
	"fmt"
	"net/http"
	"os"
	"os/signal"
	"syscall"

	"github.com/vskurikhin/DayBook-3.3x/auth/v2/internal/server/config"
	"github.com/vskurikhin/DayBook-3.3x/auth/v2/internal/server/env"
)

//go:generate mockgen -destination=server_mock_test.go -package=mocks github.com/vskurikhin/DayBook-3.3x/auth/v2/internal/server Server
type Server interface {
	Run(ctx context.Context) error
}

var _ Server = (*AuthServer)(nil)

type AuthServer struct {
	handler      http.Handler
	config       config.Values
	environments env.Environments
}

func NewAuthServer(cfg config.Config, env env.Environments, handler http.Handler) (*AuthServer, error) {
	return &AuthServer{handler: handler, config: cfg.Values(), environments: env}, nil
}

func (a AuthServer) Run(ctx context.Context) error {
	server := &http.Server{
		Addr:    a.config.Address,
		Handler: a.handler,
	}
	var errHTTPServe error
	httpExit := make(chan struct{}, 1)
	// канал для перенаправления прерываний
	// поскольку нужно отловить всего одно прерывание,
	// ёмкости 1 для канала будет достаточно
	sigQuit := make(chan os.Signal, 1)
	// регистрируем перенаправление прерываний
	signal.Notify(sigQuit, syscall.SIGINT, syscall.SIGQUIT, syscall.SIGTERM)
	go func() {
		// Start the HTTP server on port 8089 by default
		fmt.Printf("HTTPServer starting on %s...\n", a.config.Address)
		errHTTPServe = server.ListenAndServe()
		close(httpExit)
	}()
	for {
		select {
		case <-ctx.Done():
			errShutdown := server.Shutdown(ctx)
			if ctx.Err() != nil {
				return ctx.Err()
			}
			return errShutdown
		case <-httpExit:
			return errHTTPServe
		case <-sigQuit:
			return server.Shutdown(ctx)
		}
	}
}
