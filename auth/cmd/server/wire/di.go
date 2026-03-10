//go:build wireinject
// +build wireinject

package wire

import (
	"github.com/google/wire"

	"github.com/vskurikhin/DayBook-3.3x/auth/v2/internal/server"
	"github.com/vskurikhin/DayBook-3.3x/auth/v2/internal/server/config"
	"github.com/vskurikhin/DayBook-3.3x/auth/v2/internal/server/env"
	"github.com/vskurikhin/DayBook-3.3x/auth/v2/internal/server/handler"
	"github.com/vskurikhin/DayBook-3.3x/auth/v2/internal/server/resources"
)

var (
	serverSet = wire.NewSet(
		config.GetConfig,
		env.EnvironmentsLoad,
		handler.NewApiV1,
		handler.NewApiV2,
		wire.Bind(new(handler.ApiHandlers), new(*handler.Handlers)),
		wire.Bind(new(resources.ResourceV1), new(*resources.V1)),
		wire.Bind(new(resources.ResourceV2), new(*resources.V2)),
		handler.NewHandlers,
		handler.NewRouter,
		server.NewAuthServer,
		resources.NewV1,
		resources.NewV2,
	)
)

func InitializeServer(cfg config.Config, environments env.Environments) (*server.AuthServer, error) {
	wire.Build(serverSet)
	return &server.AuthServer{}, nil
}
