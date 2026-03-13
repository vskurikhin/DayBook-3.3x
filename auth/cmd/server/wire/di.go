//go:build wireinject
// +build wireinject

package wire

import (
	"github.com/google/wire"

	"github.com/vskurikhin/DayBook-3.3x/auth/v2/internal/server"
	"github.com/vskurikhin/DayBook-3.3x/auth/v2/internal/server/config"
	"github.com/vskurikhin/DayBook-3.3x/auth/v2/internal/server/db"
	"github.com/vskurikhin/DayBook-3.3x/auth/v2/internal/server/env"
	"github.com/vskurikhin/DayBook-3.3x/auth/v2/internal/server/handler"
	"github.com/vskurikhin/DayBook-3.3x/auth/v2/internal/server/resources"
)

var (
	serverSet = wire.NewSet(
		config.GetConfig,
		env.EnvironmentsLoad,
		server.NewAuthServer,
	)
	handlerSet = wire.NewSet(
		handler.NewApiV1,
		handler.NewApiV2,
		wire.Bind(new(handler.ApiHandlers), new(*handler.Handlers)),
		handler.NewHandlers,
		handler.NewRouter,
	)
	resourceSet = wire.NewSet(
		wire.Bind(new(resources.ResourceV1), new(*resources.V1)),
		wire.Bind(new(resources.ResourceV2), new(*resources.V2)),
		resources.NewV1,
		resources.NewV2,
	)
)

func InitializeServer(cfg config.Config, environments env.Environments, dbp db.DB) (*server.AuthServer, error) {
	wire.Build(serverSet, handlerSet, resourceSet)
	return &server.AuthServer{}, nil
}
