package services

import (
	"context"
	"fmt"
	"log/slog"
	"time"

	"github.com/golang-jwt/jwt/v5"
	"github.com/vskurikhin/DayBook-3.3x/auth/v2/internal/server/config"
	"github.com/vskurikhin/DayBook-3.3x/auth/v2/internal/server/repository/session"
	"github.com/vskurikhin/DayBook-3.3x/auth/v2/internal/server/repository/user_attrs"
	"github.com/vskurikhin/DayBook-3.3x/auth/v2/internal/server/xerror"
	"github.com/vskurikhin/DayBook-3.3x/auth/v2/pkg/tool"
)

type RefreshServiceV2 interface {
	Refresh(ctx context.Context, token string) (Credentials, error)
}

var _ RefreshServiceV2 = (*RefreshServiceImplV2)(nil)

type RefreshServiceImplV2 struct {
	*BaseService
	sessionRepo   session.Repo
	userAttrsRepo user_attrs.Repo
}

func (s *RefreshServiceImplV2) Refresh(ctx context.Context, token string) (Credentials, error) {
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

func (s *RefreshServiceImplV2) refresh(ctx context.Context, claims jwt.Claims) (credValuesV2, error) {
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

func NewRefreshServiceV2(
	cfg config.Config,
	sessionRepo session.Repo,
	userAttrsRepo user_attrs.Repo,
) *RefreshServiceImplV2 {
	return &RefreshServiceImplV2{
		BaseService: &BaseService{
			cfg: cfg,
		},
		sessionRepo:   sessionRepo,
		userAttrsRepo: userAttrsRepo,
	}
}
