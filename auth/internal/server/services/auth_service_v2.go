package services

import (
	"context"
	"fmt"
	"log/slog"

	"github.com/jackc/pgx/v5/pgtype"

	"github.com/vskurikhin/DayBook-3.3x/auth/v2/internal/server/config"
	"github.com/vskurikhin/DayBook-3.3x/auth/v2/internal/server/db"
	"github.com/vskurikhin/DayBook-3.3x/auth/v2/internal/server/repository/session"
	"github.com/vskurikhin/DayBook-3.3x/auth/v2/internal/server/repository/user_view"
	"github.com/vskurikhin/DayBook-3.3x/auth/v2/internal/server/xerror"
	"github.com/vskurikhin/DayBook-3.3x/auth/v2/pkg/tool"
)

type AuthServiceV2 interface {
	Auth(ctx context.Context, login Login) (Credentials, error)
}

var _ AuthServiceV2 = (*AuthServiceImplV2)(nil)

type AuthServiceImplV2 struct {
	*BaseService
	dbPool       db.DB
	sessionRepo  session.Repo
	userViewRepo user_view.Repo
}

func (s *AuthServiceImplV2) Auth(ctx context.Context, login Login) (Credentials, error) {
	return makeCredV2(s.auth(ctx, login))
}

func (s *AuthServiceImplV2) auth(ctx context.Context, login Login) (credValuesV2, error) {
	userView, err := s.userViewRepo.GetUserName(ctx, login.UserNamePgTypeText())
	if err != nil {
		slog.ErrorContext(ctx,
			"failed to get user name:",
			slog.String("error", err.Error()),
			slog.String("login", login.UserNamePgTypeText().String),
		)
		return credValuesV2{}, err
	}
	if !userView.Password.Valid {
		return credValuesV2{}, xerror.ErrPasswordNotValid
	}
	if !userView.UserName.Valid {
		return credValuesV2{}, xerror.ErrInvalidToken
	}
	if !tool.Verify(userView.Password.String, login.password) {
		return credValuesV2{}, xerror.ErrInvalidPassword
	}

	cfgValues := s.cfg.Values()
	sid, errDecodeSessionID := newSessionID(cfgValues.Hostname, userView.UserName.String)
	if errDecodeSessionID != nil {
		slog.ErrorContext(ctx,
			"failed to decode session id:",
			slog.String("error", errDecodeSessionID.Error()),
			slog.String("errorType", fmt.Sprintf("%T", errDecodeSessionID)),
		)
		return credValuesV2{}, errDecodeSessionID
	}
	return s.transactionAuth(ctx, sid, userView)
}

func (s *AuthServiceImplV2) transactionAuth(ctx context.Context, sid sessionID, user user_view.UserView) (credValuesV2, error) {
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
	cfgValues := s.cfg.Values()
	validTimePeriodsTokens := makeValidTimeTokens(cfgValues.ValidPeriodAccessToken, cfgValues.ValidPeriodRefreshToken)
	primaryKey := sid.toModelPrimaryKey()
	_, errTransaction = sessionRepoTx.CreateSession(ctx, session.CreateSessionParams{
		Iss:       primaryKey.iss,
		Jti:       primaryKey.jti,
		Sub:       primaryKey.sub,
		UserName:  pgtype.Text{String: user.UserName.String, Valid: true},
		Roles:     []string{},
		ValidTime: pgtype.Timestamptz{Time: validTimePeriodsTokens.sessionValidTime.Local(), Valid: true},
	})
	if errTransaction != nil {
		return credValuesV2{}, xerror.ClassingPgError(errTransaction)
	}
	return credValuesV2{
		secret:     s.cfg.Values().JWThs256SignKey,
		sessionID:  sid,
		timeTokens: validTimePeriodsTokens,
		user:       UserFromModelUserView(user),
	}, errTransaction
}

func NewAuthServiceV2(
	cfg config.Config,
	dbPool db.DB,
	sessionRepo session.Repo,
	userViewRepo user_view.Repo,
) *AuthServiceImplV2 {
	return &AuthServiceImplV2{
		BaseService: &BaseService{
			cfg: cfg,
		},
		dbPool:       dbPool,
		sessionRepo:  sessionRepo,
		userViewRepo: userViewRepo,
	}
}
