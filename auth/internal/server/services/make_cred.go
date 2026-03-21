package services

import (
	"encoding/base64"
	"net/http"

	"github.com/golang-jwt/jwt/v5"
)

const (
	Exp     = "exp"
	Iss     = "iss"
	Jti     = "jti"
	Refresh = "refresh"
	Sub     = "sub"
)

type credValuesV2 struct {
	secret     []byte
	sessionID  sessionID
	timeTokens validTimeTokens
	user       User
}

func makeCredV2(credValues credValuesV2, err error) (Credentials, error) {
	if err != nil {
		return Credentials{}, err
	}
	accessClaims := jwt.MapClaims{
		Exp: credValues.timeTokens.accessTokenTime.Unix(),
		Iss: credValues.sessionID.issuerUUID.String(),
		Jti: credValues.sessionID.sessionJTIUUID.String(),
		Sub: credValues.sessionID.userNameUUID.String(),
	}
	accessToken := jwt.NewWithClaims(jwt.SigningMethodHS256, accessClaims)
	signedAccessToken, errSignAccessToken := accessToken.SignedString([]byte(credValues.secret))
	if errSignAccessToken != nil {
		return Credentials{}, errSignAccessToken
	}
	refreshClaims := jwt.MapClaims{
		Exp: credValues.timeTokens.refreshTokenTime.Unix(),
		Iss: base64.StdEncoding.EncodeToString(credValues.sessionID.issuerUUID[:]),
		Jti: base64.StdEncoding.EncodeToString(credValues.sessionID.sessionJTIUUID[:]),
		Sub: base64.StdEncoding.EncodeToString(credValues.sessionID.userNameUUID[:]),
	}
	refreshToken := jwt.NewWithClaims(jwt.SigningMethodHS256, refreshClaims)
	signedRefreshToken, errSigRefreshToken := refreshToken.SignedString([]byte(credValues.secret))
	if errSigRefreshToken != nil {
		return Credentials{}, errSigRefreshToken
	}
	// Define the cookie
	cookie := http.Cookie{
		Name:     Refresh,
		Value:    signedRefreshToken,                      // The cookie's value
		Path:     "/",                                     // The path for which the cookie is valid
		Expires:  credValues.timeTokens.cookieExpiresTime, // Set the expiration time
		HttpOnly: true,                                    // Prevents JavaScript from accessing the cookie (security best practice)
		// TODO Проблема: в dev среде (http) cookie **не будет отправляться** Рекомендация: Secure: cfg.Values().IsHTTPS issue #58
		Secure:   true,                    // Ensures the cookie is only sent over HTTPS (security best practice)
		SameSite: http.SameSiteStrictMode, // Mitigates CSRF attacks
	}
	return Credentials{
		accessToken: Token{
			jwt:       signedAccessToken,
			expiresAt: credValues.timeTokens.accessTokenTime,
			user:      credValues.user,
		},
		refreshTokenCookie: cookie,
	}, nil
}
