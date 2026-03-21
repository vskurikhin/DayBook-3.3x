package services

import (
	"encoding/base64"
	"errors"
	"testing"
	"time"

	"github.com/golang-jwt/jwt/v5"
	"github.com/google/uuid"
)

func Test_makeCredV2(t *testing.T) {
	now := time.Now()

	issuer := uuid.New()
	jti := uuid.New()
	sub := uuid.New()

	cred := credValuesV2{
		secret: []byte("test-secret"),
		sessionID: sessionID{
			issuerUUID:     issuer,
			sessionJTIUUID: jti,
			userNameUUID:   sub,
		},
		timeTokens: validTimeTokens{
			accessTokenTime:   now.Add(time.Hour),
			refreshTokenTime:  now.Add(2 * time.Hour),
			cookieExpiresTime: now.Add(2 * time.Hour),
		},
		user: User{
			userName: "test-user",
		},
	}

	tests := []struct {
		name      string
		input     credValuesV2
		err       error
		wantError bool
		check     func(t *testing.T, got Credentials)
	}{
		{
			name:      "error input",
			input:     cred,
			err:       errors.New("some error"),
			wantError: true,
		},
		{
			name:      "success",
			input:     cred,
			err:       nil,
			wantError: false,
			check: func(t *testing.T, got Credentials) {
				if got.accessToken.jwt == "" {
					t.Fatal("access token is empty")
				}

				// Парсим access token
				parsed, err := jwt.Parse(got.accessToken.jwt, func(token *jwt.Token) (interface{}, error) {
					return cred.secret, nil
				})
				if err != nil {
					t.Fatalf("failed to parse access token: %v", err)
				}

				claims, ok := parsed.Claims.(jwt.MapClaims)
				if !ok {
					t.Fatal("invalid claims type")
				}

				if claims[Iss] != issuer.String() {
					t.Errorf("unexpected iss: %v", claims[Iss])
				}
				if claims[Jti] != jti.String() {
					t.Errorf("unexpected jti: %v", claims[Jti])
				}
				if claims[Sub] != sub.String() {
					t.Errorf("unexpected sub: %v", claims[Sub])
				}

				// Проверка cookie
				cookie := got.refreshTokenCookie
				if cookie.Name != Refresh {
					t.Errorf("unexpected cookie name: %v", cookie.Name)
				}
				if cookie.Value == "" {
					t.Fatal("empty refresh token cookie")
				}
				if !cookie.HttpOnly {
					t.Error("cookie should be HttpOnly")
				}
				if !cookie.Secure {
					t.Error("cookie should be Secure")
				}

				// Парсим refresh token
				refreshParsed, err := jwt.Parse(cookie.Value, func(token *jwt.Token) (interface{}, error) {
					return cred.secret, nil
				})
				if err != nil {
					t.Fatalf("failed to parse refresh token: %v", err)
				}

				refreshClaims, ok := refreshParsed.Claims.(jwt.MapClaims)
				if !ok {
					t.Fatal("invalid refresh claims type")
				}

				expectedIss := base64.StdEncoding.EncodeToString(issuer[:])
				expectedJti := base64.StdEncoding.EncodeToString(jti[:])
				expectedSub := base64.StdEncoding.EncodeToString(sub[:])

				if refreshClaims[Iss] != expectedIss {
					t.Errorf("unexpected refresh iss: %v", refreshClaims[Iss])
				}
				if refreshClaims[Jti] != expectedJti {
					t.Errorf("unexpected refresh jti: %v", refreshClaims[Jti])
				}
				if refreshClaims[Sub] != expectedSub {
					t.Errorf("unexpected refresh sub: %v", refreshClaims[Sub])
				}
			},
		},
		{
			name: "sign error (empty secret)",
			input: credValuesV2{
				secret:     []byte{}, // может вызвать ошибку подписи
				sessionID:  cred.sessionID,
				timeTokens: cred.timeTokens,
				user:       cred.user,
			},
			err:       nil,
			wantError: false, // jwt обычно всё равно подпишет, но кейс оставляем
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := makeCredV2(tt.input, tt.err)

			if tt.wantError {
				if err == nil {
					t.Fatal("expected error, got nil")
				}
				return
			}

			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}

			if tt.check != nil {
				tt.check(t, got)
			}
		})
	}
}
