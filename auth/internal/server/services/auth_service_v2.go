package services

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"time"

	"github.com/go-chi/jwtauth/v5"
	"github.com/golang-jwt/jwt/v5"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/vskurikhin/DayBook-3.3x/auth/v2/internal/server/config"
	"github.com/vskurikhin/DayBook-3.3x/auth/v2/internal/server/db"
	"github.com/vskurikhin/DayBook-3.3x/auth/v2/internal/server/repository/session"
	"github.com/vskurikhin/DayBook-3.3x/auth/v2/internal/server/repository/user_attrs"
	"github.com/vskurikhin/DayBook-3.3x/auth/v2/internal/server/repository/user_name"
	"github.com/vskurikhin/DayBook-3.3x/auth/v2/internal/server/repository/user_view"
	"github.com/vskurikhin/DayBook-3.3x/auth/v2/internal/server/xerror"
	"github.com/vskurikhin/DayBook-3.3x/auth/v2/pkg/tool"
)

//go:generate mockgen -destination=config_mock_test.go -package=services github.com/vskurikhin/DayBook-3.3x/auth/v2/internal/server/services Config
type Config interface {
	JWThs256SignKey(string)
	Values() config.Values
}

//go:generate mockgen -destination=session_repo_mock_test.go -package=services github.com/vskurikhin/DayBook-3.3x/auth/v2/internal/server/services SessionRepo
type SessionRepo interface {
	CreateSession(ctx context.Context, arg session.CreateSessionParams) (session.Session, error)
	DeleteSession(ctx context.Context, arg session.DeleteSessionParams) error
	GetSession(ctx context.Context, arg session.GetSessionParams) (session.Session, error)
	ListSessions(ctx context.Context) ([]session.Session, error)
	UpdateSession(ctx context.Context, arg session.UpdateSessionParams) error
	WithTx(tx pgx.Tx) *session.Queries
}

//go:generate mockgen -destination=user_attrs_repo_mock_test.go -package=services github.com/vskurikhin/DayBook-3.3x/auth/v2/internal/server/services UserAttrsRepo
type UserAttrsRepo interface {
	CreateUserAttrs(ctx context.Context, arg user_attrs.CreateUserAttrsParams) (user_attrs.UserAttr, error)
	DeleteUserAttrs(ctx context.Context, userName string) error
	GetUserAttrs(ctx context.Context, userName string) (user_attrs.UserAttr, error)
	ListUserAttrs(ctx context.Context) ([]user_attrs.UserAttr, error)
	UpdateUserAttrs(ctx context.Context, arg user_attrs.UpdateUserAttrsParams) error
	WithTx(tx pgx.Tx) *user_attrs.Queries
}

//go:generate mockgen -destination=user_name_repo_mock_test.go -package=services github.com/vskurikhin/DayBook-3.3x/auth/v2/internal/server/services UserNameRepo
type UserNameRepo interface {
	CreateUserName(ctx context.Context, arg user_name.CreateUserNameParams) (user_name.UserName, error)
	DeleteUserNameByID(ctx context.Context, id pgtype.UUID) error
	DeleteUserNameByName(ctx context.Context, userName string) error
	GetUserName(ctx context.Context, userName string) (user_name.UserName, error)
	ListUserNames(ctx context.Context) ([]user_name.UserName, error)
	UpdateUserName(ctx context.Context, arg user_name.UpdateUserNameParams) error
	WithTx(tx pgx.Tx) *user_name.Queries
}

//go:generate mockgen -destination=user_view_repo_mock_test.go -package=services github.com/vskurikhin/DayBook-3.3x/auth/v2/internal/server/services UserViewRepo
type UserViewRepo interface {
	GetUserName(ctx context.Context, userName pgtype.Text) (user_view.UserView, error)
	ListUserNames(ctx context.Context) ([]user_view.UserView, error)
}

type AuthServiceV2 interface {
	AuthServiceV1
	Auth(ctx context.Context, login Login) (Credentials, error)
	List(ctx context.Context) ([]User, error)
	Logout(ctx context.Context) error
	Refresh(ctx context.Context, token string) (Credentials, error)
	Register(ctx context.Context, user CreateUser) (Credentials, error)
}

var _ AuthServiceV2 = (*ServiceV2)(nil)

type ServiceV2 struct {
	*ServiceV1
	sessionRepo   session.Repo
	userAttrsRepo user_attrs.Repo
	userNameRepo  user_name.Repo
	userViewRepo  user_view.Repo
}

func (s *ServiceV2) Auth(ctx context.Context, login Login) (Credentials, error) {
	return makeCredV2(s.auth(ctx, login))
}

func (s *ServiceV2) List(ctx context.Context) ([]User, error) {
	list, err := s.userAttrsRepo.ListUserAttrs(ctx)
	if err != nil {
		return nil, err
	}
	return tool.Map(list, UserFromModelUserAttr), err
}

func (s *ServiceV2) Logout(ctx context.Context) error {
	bearerToken, _, errBearerToken := jwtauth.FromContext(ctx)
	if errBearerToken != nil {
		slog.ErrorContext(ctx,
			"failed to parse bearer",
			slog.String("error", errBearerToken.Error()),
			slog.String("errorType", fmt.Sprintf("%T", errBearerToken)),
		)
		return errBearerToken
	}

	_, _ = fmt.Fprintf(os.Stderr, "bearerToken: %v\n", bearerToken)
	sid, errDecodeSessionID := sessionIDFromJwxToken(bearerToken)
	_, _ = fmt.Fprintf(os.Stderr, "errDecodeSessionID: %v\n", errDecodeSessionID)
	if errDecodeSessionID != nil {
		slog.ErrorContext(ctx,
			"failed to decode sessionID",
			slog.String("error", errDecodeSessionID.Error()),
			slog.String("errorType", fmt.Sprintf("%T", errDecodeSessionID)),
		)
		return errDecodeSessionID
	}
	primaryKey := sid.toModelPrimaryKey()

	return s.sessionRepo.DeleteSession(ctx, session.DeleteSessionParams{
		Iss: primaryKey.iss,
		Jti: primaryKey.jti,
		Sub: primaryKey.sub,
	})
}

func (s *ServiceV2) Refresh(ctx context.Context, token string) (Credentials, error) {
	parsedToken, err := jwt.ParseWithClaims(token, &tool.CustomClaims{}, func(token *jwt.Token) (interface{}, error) {
		if _, ok := token.Method.(*jwt.SigningMethodHMAC); !ok {
			return nil, fmt.Errorf("unexpected signing method: %v", token.Header["alg"])
		}
		return []byte(s.cfg.Values().JWThs256SignKey), nil
	})
	if err != nil {
		slog.ErrorContext(ctx,
			"failed to parse token",
			slog.String("error", err.Error()),
			slog.String("errorType", fmt.Sprintf("%T", err)),
		)
		return Credentials{}, err
	}
	return makeCredV2(s.refresh(ctx, parsedToken.Claims))
}

func (s *ServiceV2) Register(ctx context.Context, user CreateUser) (Credentials, error) {
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

func (s *ServiceV2) auth(ctx context.Context, login Login) (credValuesV2, error) {
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

func (s *ServiceV2) refresh(ctx context.Context, claims jwt.Claims) (credValuesV2, error) {
	sid, errDecodeSessionID := sessionIDFromClaims(claims)
	if errDecodeSessionID != nil {
		slog.ErrorContext(ctx,
			"failed to decode sessionID",
			slog.String("error", errDecodeSessionID.Error()),
			slog.String("errorType", fmt.Sprintf("%T", errDecodeSessionID)),
		)
		return credValuesV2{}, errDecodeSessionID
	}

	primaryKey := sid.toModelPrimaryKey()
	sess, errGetSession := s.sessionRepo.GetSession(ctx, session.GetSessionParams{
		Iss: primaryKey.iss,
		Jti: primaryKey.jti,
		Sub: primaryKey.sub,
	})
	if errGetSession != nil {
		return credValuesV2{}, xerror.ClassingPgError(errGetSession)
	}
	if sess.Jti != primaryKey.jti {
		return credValuesV2{}, xerror.ErrInvalidToken
	}
	if !sess.UserName.Valid {
		return credValuesV2{}, xerror.ErrJInvalidUserName
	}
	if !sess.ValidTime.Valid {
		return credValuesV2{}, xerror.ErrSessionTimeExpired
	}

	cfgValues := s.cfg.Values()
	if time.Now().Add(cfgValues.ValidPeriodAccessToken).After(sess.ValidTime.Time) {
		slog.ErrorContext(ctx,
			"session expired",
			slog.String("error", xerror.ErrSessionTimeExpired.Error()),
			slog.String("errorType", fmt.Sprintf("%T", xerror.ErrSessionTimeExpired)),
		)
		err := s.sessionRepo.DeleteSession(ctx, session.DeleteSessionParams{
			Iss: primaryKey.iss,
			Jti: primaryKey.jti,
			Sub: primaryKey.sub,
		})
		if err != nil {
			slog.ErrorContext(ctx,
				"failed to delete session",
				slog.String("error", err.Error()),
				slog.String("errorType", fmt.Sprintf("%T", err)),
			)
			return credValuesV2{}, xerror.ErrSessionTimeExpired
		}
		return credValuesV2{}, xerror.ErrSessionTimeExpired
	}

	user, err := s.userAttrsRepo.GetUserAttrs(ctx, sess.UserName.String)
	if err != nil {
		slog.ErrorContext(ctx,
			"failed to get user attributes",
			slog.String("error", err.Error()),
			slog.String("errorType", fmt.Sprintf("%T", err)),
		)
		return credValuesV2{}, err
	}
	validPeriodAccessToken := cfgValues.ValidPeriodAccessToken
	if cfgValues.ValidPeriodAccessToken > sess.ValidTime.Time.Sub(time.Now().Add(-time.Second)) {
		validPeriodAccessToken = sess.ValidTime.Time.Sub(time.Now().Add(-time.Second))
	}
	validTimePeriodsTokens := makeValidTimeTokens(validPeriodAccessToken, sess.ValidTime.Time.Sub(time.Now()))

	return credValuesV2{
		secret:     s.cfg.Values().JWThs256SignKey,
		sessionID:  sid,
		timeTokens: validTimePeriodsTokens,
		user:       UserFromModelUserAttr(user),
	}, nil
}

func (s *ServiceV2) transactionAuth(ctx context.Context, sid sessionID, user user_view.UserView) (credValuesV2, error) {
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

func (s *ServiceV2) transactionRegister(ctx context.Context, user CreateUser) (credValuesV2, error) {
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

func NewAuthServiceV2(
	serviceV1 *ServiceV1,
	sessionRepo session.Repo,
	userAttrsRepo user_attrs.Repo,
	userNameRepo user_name.Repo,
	userViewRepo user_view.Repo,
) *ServiceV2 {
	return &ServiceV2{
		ServiceV1:     serviceV1,
		sessionRepo:   sessionRepo,
		userAttrsRepo: userAttrsRepo,
		userNameRepo:  userNameRepo,
		userViewRepo:  userViewRepo,
	}
}

func OldAuthServiceV2(
	cfg config.Config,
	dbPool db.DB,
	sessionRepo session.Repo,
	userAttrsRepo user_attrs.Repo,
	userNameRepo user_name.Repo,
	userViewRepo user_view.Repo,
) *ServiceV2 {
	return &ServiceV2{
		ServiceV1:     NewAuthServiceV1(cfg, dbPool),
		sessionRepo:   sessionRepo,
		userAttrsRepo: userAttrsRepo,
		userNameRepo:  userNameRepo,
		userViewRepo:  userViewRepo,
	}
}
