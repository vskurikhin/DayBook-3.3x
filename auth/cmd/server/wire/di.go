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
	"github.com/vskurikhin/DayBook-3.3x/auth/v2/internal/server/repository/user_name"
	"github.com/vskurikhin/DayBook-3.3x/auth/v2/internal/server/repository/user_view"
	"github.com/vskurikhin/DayBook-3.3x/auth/v2/internal/server/resources"
	"github.com/vskurikhin/DayBook-3.3x/auth/v2/internal/server/services"
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
	serviceSet = wire.NewSet(
		wire.Bind(new(services.BaseService), new(*services.AuthBaseService)),
		services.NewAuthBaseService,
	)
	repositorySet = wire.NewSet(
		wire.Bind(new(user_name.Repo), new(*user_name.Queries)),
		wire.Bind(new(user_name.DBTX), new(*db.PgxPool)),
		wire.Bind(new(user_view.Repo), new(*user_view.Queries)),
		wire.Bind(new(user_view.DBTX), new(*db.PgxPool)),
		db.GetDB,
		user_name.New,
		user_view.New,
	)
)

func InitializeServer(cfg config.Config, environments env.Environments) (*server.AuthServer, error) {
	wire.Build(serverSet, handlerSet, resourceSet, serviceSet, repositorySet)
	return &server.AuthServer{}, nil
}
