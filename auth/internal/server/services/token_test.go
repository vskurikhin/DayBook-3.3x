package services

import (
	"net/http"
	"reflect"
	"testing"
	"time"
)

func TestCredentials_AccessToken(t *testing.T) {
	now := time.Now()

	token := Token{
		expiresAt: now,
		jwt:       "test-jwt",
		user: User{
			userName: "test-user",
		},
	}

	tests := []struct {
		name string
		cred Credentials
		want Token
	}{
		{
			name: "returns access token",
			cred: Credentials{
				accessToken: token,
			},
			want: token,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := tt.cred.AccessToken()

			if !reflect.DeepEqual(got, tt.want) {
				t.Errorf("AccessToken() = %+v, want %+v", got, tt.want)
			}
		})
	}
}

func TestCredentials_RefreshTokenCookie(t *testing.T) {
	cookie := http.Cookie{
		Name:     "refresh",
		Value:    "token",
		HttpOnly: true,
		Secure:   true,
	}

	tests := []struct {
		name string
		cred Credentials
		want http.Cookie
	}{
		{
			name: "returns refresh cookie",
			cred: Credentials{
				refreshTokenCookie: cookie,
			},
			want: cookie,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := tt.cred.RefreshTokenCookie()

			if !reflect.DeepEqual(got, tt.want) {
				t.Errorf("RefreshTokenCookie() = %+v, want %+v", got, tt.want)
			}
		})
	}
}

func TestToken_ToDto(t *testing.T) {
	now := time.Now()

	user := User{
		userName: "test-user",
		email:    "test@example.com",
	}

	tests := []struct {
		name  string
		token Token
		check func(t *testing.T, dtoToken interface{})
	}{
		{
			name: "valid conversion",
			token: Token{
				expiresAt: now,
				jwt:       "jwt-token",
				user:      user,
			},
			check: func(t *testing.T, dtoToken interface{}) {
				got := dtoToken.(struct {
					ExpiresAt time.Time
					JWT       string
					User      interface{}
				})

				if got.JWT != "jwt-token" {
					t.Errorf("unexpected JWT: %v", got.JWT)
				}
				if !got.ExpiresAt.Equal(now) {
					t.Errorf("unexpected ExpiresAt: %v", got.ExpiresAt)
				}
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			dtoToken := tt.token.ToDto()

			// Проверяем базовые поля напрямую
			if dtoToken.JWT != tt.token.jwt {
				t.Errorf("JWT mismatch: got %v, want %v", dtoToken.JWT, tt.token.jwt)
			}

			if !dtoToken.ExpiresAt.Equal(tt.token.expiresAt) {
				t.Errorf("ExpiresAt mismatch")
			}

			// User.ToDto() проверяем косвенно (не nil)
			if reflect.ValueOf(dtoToken.User).IsZero() {
				t.Error("User DTO should not be zero")
			}
		})
	}
}
