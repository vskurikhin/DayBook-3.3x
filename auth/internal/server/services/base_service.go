package services

import (
	"context"
	"errors"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/vskurikhin/DayBook-3.3x/auth/v2/internal/server/config"
	"github.com/vskurikhin/DayBook-3.3x/auth/v2/internal/server/repository/session"
	"github.com/vskurikhin/DayBook-3.3x/auth/v2/internal/server/repository/user_attrs"
	"github.com/vskurikhin/DayBook-3.3x/auth/v2/internal/server/repository/user_name"
	"github.com/vskurikhin/DayBook-3.3x/auth/v2/internal/server/repository/user_view"
	"github.com/vskurikhin/DayBook-3.3x/auth/v2/internal/server/xerror"
)

//go:generate mockgen -destination=z_mock_config_test.go -package=services github.com/vskurikhin/DayBook-3.3x/auth/v2/internal/server/services Config
type Config interface {
	JWThs256SignKey(string)
	Values() config.Values
}

//go:generate mockgen -destination=z_mock_session_repo_test.go -package=services github.com/vskurikhin/DayBook-3.3x/auth/v2/internal/server/services SessionRepo
type SessionRepo interface {
	CreateSession(ctx context.Context, arg session.CreateSessionParams) (session.Session, error)
	DeleteSession(ctx context.Context, arg session.DeleteSessionParams) error
	GetSession(ctx context.Context, arg session.GetSessionParams) (session.Session, error)
	ListSessions(ctx context.Context) ([]session.Session, error)
	UpdateSession(ctx context.Context, arg session.UpdateSessionParams) error
	WithTx(tx pgx.Tx) *session.Queries
}

//go:generate mockgen -destination=z_mock_user_attrs_repo_test.go -package=services github.com/vskurikhin/DayBook-3.3x/auth/v2/internal/server/services UserAttrsRepo
type UserAttrsRepo interface {
	CreateUserAttrs(ctx context.Context, arg user_attrs.CreateUserAttrsParams) (user_attrs.UserAttr, error)
	DeleteUserAttrs(ctx context.Context, userName string) error
	GetUserAttrs(ctx context.Context, userName string) (user_attrs.UserAttr, error)
	ListUserAttrs(ctx context.Context) ([]user_attrs.UserAttr, error)
	UpdateUserAttrs(ctx context.Context, arg user_attrs.UpdateUserAttrsParams) error
	WithTx(tx pgx.Tx) *user_attrs.Queries
}

//go:generate mockgen -destination=z_mock_user_name_repo_test.go -package=services github.com/vskurikhin/DayBook-3.3x/auth/v2/internal/server/services UserNameRepo
type UserNameRepo interface {
	CreateUserName(ctx context.Context, arg user_name.CreateUserNameParams) (user_name.UserName, error)
	DeleteUserNameByID(ctx context.Context, id pgtype.UUID) error
	DeleteUserNameByName(ctx context.Context, userName string) error
	GetUserName(ctx context.Context, userName string) (user_name.UserName, error)
	ListUserNames(ctx context.Context) ([]user_name.UserName, error)
	UpdateUserName(ctx context.Context, arg user_name.UpdateUserNameParams) error
	WithTx(tx pgx.Tx) *user_name.Queries
}

//go:generate mockgen -destination=z_mock_user_view_repo_test.go -package=services github.com/vskurikhin/DayBook-3.3x/auth/v2/internal/server/services UserViewRepo
type UserViewRepo interface {
	GetUserName(ctx context.Context, userName pgtype.Text) (user_view.UserView, error)
	ListUserNames(ctx context.Context) ([]user_view.UserView, error)
}

type BaseService struct {
	cfg config.Config
}

var (
	ErrInvalidUserID = errors.New(xerror.InvalidUserID)
)
