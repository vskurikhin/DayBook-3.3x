package services

import (
	"net/http"
	"time"

	"github.com/vskurikhin/DayBook-3.3x/auth/v2/internal/server/dto"
)

type Token struct {
	expiresAt time.Time
	jwt       string
	user      User
}

type Creds struct {
	accessToken        Token
	refreshTokenCookie http.Cookie
	expiresAt          time.Time
}

func (c Creds) AccessToken() Token {
	return c.accessToken
}

func (c Creds) RefreshTokenCookie() http.Cookie {
	return c.refreshTokenCookie
}

func (t Token) ToDto() dto.Token {
	return dto.Token{
		ExpiresAt: t.expiresAt,
		JWT:       t.jwt,
		User:      t.user.ToDto(),
	}
}
