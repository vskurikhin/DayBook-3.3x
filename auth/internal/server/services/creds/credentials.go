package creds

import (
	"encoding/base64"
	"net/http"
	"time"

	"github.com/golang-jwt/jwt/v5"
	"github.com/vskurikhin/DayBook-3.3x/auth/v2/internal/server/config"
	"github.com/vskurikhin/DayBook-3.3x/auth/v2/internal/server/services/model"
	"github.com/vskurikhin/DayBook-3.3x/auth/v2/pkg/tool"
)

const (
	Exp     = "exp"
	Iss     = "iss"
	Jti     = "jti"
	Refresh = "refresh"
	Sub     = "sub"
)

//go:generate mockgen -destination=mock_config_test.go -package=creds github.com/vskurikhin/DayBook-3.3x/auth/v2/internal/server/services/creds Config
type Config interface {
	JWThs256SignKey(string)
	Values() config.Values
}

type CredentialsFactoryV2 interface {
	MakeCredentials(credValues model.CredValuesV2, err error) (model.Credentials, error)
}

var _ CredentialsFactoryV2 = (*CredentialsMethodFactoryV2)(nil)

type CredentialsMethodFactoryV2 struct {
	secret []byte
}

func (c *CredentialsMethodFactoryV2) MakeCredentials(credValues model.CredValuesV2, err error) (model.Credentials, error) {
	if err != nil {
		return model.Credentials{}, err
	}
	accessClaims := jwt.MapClaims{
		Exp: credValues.TimeTokens().AccessTokenTime().Unix(),
		Iss: credValues.SessionID().IssuerUUID().String(),
		Jti: credValues.SessionID().SessionJTIUUID().String(),
		Sub: credValues.SessionID().UserNameUUID().String(),
	}
	accessToken := jwt.NewWithClaims(jwt.SigningMethodHS256, accessClaims)
	signedAccessToken, errSignAccessToken := accessToken.SignedString(c.secret)
	if errSignAccessToken != nil {
		return model.Credentials{}, errSignAccessToken
	}
	refreshClaims := jwt.MapClaims{
		Exp: credValues.TimeTokens().RefreshTokenTime().Unix(),
		Iss: base64.StdEncoding.EncodeToString(tool.UUIDToSlice(credValues.SessionID().IssuerUUID())),
		Jti: base64.StdEncoding.EncodeToString(tool.UUIDToSlice(credValues.SessionID().SessionJTIUUID())),
		Sub: base64.StdEncoding.EncodeToString(tool.UUIDToSlice(credValues.SessionID().UserNameUUID())),
	}
	refreshToken := jwt.NewWithClaims(jwt.SigningMethodHS256, refreshClaims)
	signedRefreshToken, errSigRefreshToken := refreshToken.SignedString(c.secret)
	if errSigRefreshToken != nil {
		return model.Credentials{}, errSigRefreshToken
	}
	// Define the cookie
	cookie := http.Cookie{
		Name:     Refresh,
		Value:    signedRefreshToken,                          // The cookie's value
		Path:     "/",                                         // The path for which the cookie is valid
		Expires:  credValues.TimeTokens().CookieExpiresTime(), // Set the expiration time
		HttpOnly: true,                                        // Prevents JavaScript from accessing the cookie (security best practice)
		// TODO Проблема: в dev среде (http) cookie **не будет отправляться** Рекомендация: Secure: cfg.Values().IsHTTPS issue #58
		Secure:   true,                    // Ensures the cookie is only sent over HTTPS (security best practice)
		SameSite: http.SameSiteStrictMode, // Mitigates CSRF attacks
	}
	return model.MakeCredentials(
		model.MakeToken(credValues.TimeTokens().AccessTokenTime(), signedAccessToken, credValues.User()),
		cookie,
		time.Now(),
	), nil
}

func NewCredentialsMethodFactory(cfg config.Config) *CredentialsMethodFactoryV2 {
	return &CredentialsMethodFactoryV2{
		secret: cfg.Values().JWThs256SignKey,
	}
}
