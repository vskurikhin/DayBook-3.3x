package services

import (
	"context"
	"fmt"
	"log/slog"

	"github.com/go-chi/jwtauth/v5"
	"github.com/vskurikhin/DayBook-3.3x/auth/v2/internal/server/config"
	"github.com/vskurikhin/DayBook-3.3x/auth/v2/internal/server/repository/session"
)

type LogoutServiceV2 interface {
	Logout(ctx context.Context) error
}

var _ LogoutServiceV2 = (*LogoutServiceImplV2)(nil)

type LogoutServiceImplV2 struct {
	*BaseService
	sessionRepo session.Repo
}

func (s *LogoutServiceImplV2) Logout(ctx context.Context) error {
	bearerToken, _, errBearerToken := jwtauth.FromContext(ctx)
	if errBearerToken != nil {
		slog.ErrorContext(ctx,
			"failed to parse bearer",
			slog.String("error", errBearerToken.Error()),
			slog.String("errorType", fmt.Sprintf("%T", errBearerToken)),
		)
		return errBearerToken
	}

	sid, errDecodeSessionID := sessionIDFromJwxToken(bearerToken)
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

func NewLogoutServiceV2(cfg config.Config, sessionRepo session.Repo) *LogoutServiceImplV2 {
	return &LogoutServiceImplV2{
		BaseService: &BaseService{
			cfg: cfg,
		},
		sessionRepo: sessionRepo,
	}
}
