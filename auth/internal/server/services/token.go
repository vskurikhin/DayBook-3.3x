package services

import "github.com/vskurikhin/DayBook-3.3x/auth/v2/internal/server/dto"

type Token struct {
	jwt string
}

func TokenFromDto(token dto.Token) Token {
	return Token{
		jwt: token.JWT,
	}
}

func (t Token) ToDto() dto.Token {
	return dto.Token{
		JWT: t.jwt,
	}
}
