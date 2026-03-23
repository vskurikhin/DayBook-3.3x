package services

import (
	"context"
	"fmt"
	"log/slog"

	"github.com/jackc/pgx/v5/pgtype"

	"github.com/vskurikhin/DayBook-3.3x/auth/v2/internal/server/config"
	"github.com/vskurikhin/DayBook-3.3x/auth/v2/internal/server/db"
	"github.com/vskurikhin/DayBook-3.3x/auth/v2/internal/server/repository/session"
	"github.com/vskurikhin/DayBook-3.3x/auth/v2/internal/server/repository/user_attrs"
	"github.com/vskurikhin/DayBook-3.3x/auth/v2/internal/server/repository/user_name"
	"github.com/vskurikhin/DayBook-3.3x/auth/v2/internal/server/xerror"
	"github.com/vskurikhin/DayBook-3.3x/auth/v2/pkg/tool"
)

type RegisterServiceV2 interface {
	Register(ctx context.Context, user CreateUser) (Credentials, error)
}

var _ RegisterServiceV2 = (*RegisterServiceImplV2)(nil)

type RegisterServiceImplV2 struct {
	*BaseService
	dbPool        db.DB
	sessionRepo   session.Repo
	userAttrsRepo user_attrs.Repo
	userNameRepo  user_name.Repo
}

func (s *RegisterServiceImplV2) Register(ctx context.Context, user CreateUser) (Credentials, error) {
	var err error
	user.password, err = tool.Hash(user.password, int(s.cfg.Values().AuthCost))
	if err != nil {
		slog.ErrorContext(ctx,
			"failed to hash password",
			slog.String("error", err.Error()),
			slog.String("errorType", fmt.Sprintf("%T", err)),
		)
		return Credentials{}, err
	}
	return makeCredV2(s.transactionRegister(ctx, user))
}

func (s *RegisterServiceImplV2) transactionRegister(ctx context.Context, user CreateUser) (credValuesV2, error) {
	var errTransaction = xerror.ErrNil
	tx, errTransaction := s.dbPool.Begin(ctx)
	if errTransaction != nil {
		slog.ErrorContext(ctx,
			"failed to begin transaction",
			slog.String("error", errTransaction.Error()),
			slog.String("errorType", fmt.Sprintf("%T", errTransaction)),
		)
		return credValuesV2{}, errTransaction
	}
	defer func() { deferTransaction(ctx, tx, errTransaction) }()

	sessionRepoTx := s.sessionRepo.WithTx(tx)
	userAttrsRepoTx := s.userAttrsRepo.WithTx(tx)
	userNameRepoTx := s.userNameRepo.WithTx(tx)
	userName, errTransaction := userNameRepoTx.CreateUserName(ctx, user.ToModelCreateUserNameParams())

	if errTransaction != nil {
		switch xerror.ClassingPgError(errTransaction) {
		case xerror.ErrUniqueViolation:
			errTransaction = xerror.ErrUserExists
		default:
			errTransaction = xerror.ClassingPgError(errTransaction)
		}
		return credValuesV2{}, errTransaction
	}
	if !userName.ID.Valid {
		errTransaction = ErrInvalidUserID
		return credValuesV2{}, errTransaction
	}

	user.id = userName.ID
	_, errTransaction = userAttrsRepoTx.CreateUserAttrs(ctx, user.ToModelCreateUserAttrsParams())
	if errTransaction != nil {
		return credValuesV2{}, xerror.ClassingPgError(errTransaction)
	}

	cfgValues := s.cfg.Values()
	sid, errTransaction := newSessionID(cfgValues.Hostname, userName.UserName)
	if errTransaction != nil {
		return credValuesV2{}, errTransaction
	}

	validPeriodAccessToken := makeValidTimeTokens(cfgValues.ValidPeriodAccessToken, cfgValues.ValidPeriodRefreshToken)
	primaryKey := sid.toModelPrimaryKey()
	_, errTransaction = sessionRepoTx.CreateSession(ctx, session.CreateSessionParams{
		Iss:       primaryKey.iss,
		Jti:       primaryKey.jti,
		Sub:       primaryKey.sub,
		UserName:  pgtype.Text{String: userName.UserName, Valid: true},
		Roles:     []string{},
		ValidTime: pgtype.Timestamptz{Time: validPeriodAccessToken.sessionValidTime.Local(), Valid: true},
	})
	if errTransaction != nil {
		return credValuesV2{}, xerror.ClassingPgError(errTransaction)
	}
	userResult := UserFromModelUserName(userName)
	userResult.name = user.name
	userResult.email = user.email

	return credValuesV2{
		secret:     s.cfg.Values().JWThs256SignKey,
		sessionID:  sid,
		timeTokens: validPeriodAccessToken,
		user:       userResult,
	}, errTransaction
}

func NewRegisterServiceV2(
	cfg config.Config,
	sessionRepo session.Repo,
	userAttrsRepo user_attrs.Repo,
	userNameRepo user_name.Repo,
) *RegisterServiceImplV2 {
	return &RegisterServiceImplV2{
		BaseService: &BaseService{
			cfg: cfg,
		},
		sessionRepo:   sessionRepo,
		userAttrsRepo: userAttrsRepo,
		userNameRepo:  userNameRepo,
	}
}
