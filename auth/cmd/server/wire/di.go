//go:build wireinject
// +build wireinject

package wire

import (
	"github.com/google/wire"
	"github.com/vskurikhin/DayBook-3.3x/auth/v2/internal/server"
	"github.com/vskurikhin/DayBook-3.3x/auth/v2/internal/server/config"
	"github.com/vskurikhin/DayBook-3.3x/auth/v2/internal/server/handler"
)

var (
	serverSet = wire.NewSet(
		config.GetConfig,
		server.NewAuthServer,
		handler.NewRouter,
	)
)

func InitializeServer(cfg config.Config) *server.AuthServer {
	wire.Build(serverSet)
	return &server.AuthServer{}
}
