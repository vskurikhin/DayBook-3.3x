package services

import (
	"github.com/google/uuid"
	jwx "github.com/lestrrat-go/jwx/v3/jwt"
)

func newTestJWXToken() jwx.Token {
	token := jwx.New()
	_ = token.Set(jwx.IssuerKey, uuid.New().String())
	_ = token.Set(jwx.SubjectKey, uuid.New().String())
	_ = token.Set(jwx.JwtIDKey, uuid.New().String())
	return token
}
