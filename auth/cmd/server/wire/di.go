//go:build wireinject
// +build wireinject

package wire

import (
	"github.com/google/wire"
	"github.com/vskurikhin/DayBook-3.3x/auth/v2/internal/server"
	"github.com/vskurikhin/DayBook-3.3x/auth/v2/internal/server/config"
)

var (
	serverSet = wire.NewSet(
		config.GetConfig,
		server.NewAuthServer,
	)
)

func InitializeServer(cfg config.Config) *server.AuthServer {
	wire.Build(serverSet)
	return &server.AuthServer{}
}
