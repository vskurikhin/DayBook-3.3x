package services

import (
	"testing"

	"github.com/golang-jwt/jwt/v5"
	"github.com/google/uuid"
	jwx "github.com/lestrrat-go/jwx/v3/jwt"
)

func Test_newSessionID(t *testing.T) {
	tests := []struct {
		name     string
		hostname string
		username string
	}{
		{
			name:     "valid input",
			hostname: "example.com",
			username: "user",
		},
		{
			name:     "empty values",
			hostname: "",
			username: "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			sid, err := newSessionID(tt.hostname, tt.username)
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}

			if sid.issuerUUID == uuid.Nil {
				t.Error("issuerUUID should not be nil")
			}
			if sid.userNameUUID == uuid.Nil {
				t.Error("userNameUUID should not be nil")
			}
			if sid.sessionJTIUUID == uuid.Nil {
				t.Error("sessionJTIUUID should not be nil")
			}
		})
	}
}

func Test_sessionIDFromClaims(t *testing.T) {
	validIssuer := uuid.New()
	validSubject := uuid.New()
	validJTI := uuid.New()

	tests := []struct {
		name      string
		claims    jwt.Claims
		wantError bool
	}{
		{
			name: "invalid issuer",
			claims: jwt.MapClaims{
				"iss": "invalid",
				"sub": validSubject.String(),
				"jti": validJTI.String(),
			},
			wantError: true,
		},
		{
			name: "invalid subject",
			claims: jwt.MapClaims{
				"iss": validIssuer.String(),
				"sub": "invalid",
				"jti": validJTI.String(),
			},
			wantError: true,
		},
		{
			name: "invalid jti",
			claims: jwt.MapClaims{
				"iss": validIssuer.String(),
				"sub": validSubject.String(),
				"jti": "invalid",
			},
			wantError: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			sid, err := sessionIDFromClaims(tt.claims)

			if tt.wantError {
				if err == nil {
					t.Fatal("expected error, got nil")
				}
				return
			}

			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}

			if sid.issuerUUID.String() != tt.claims.(jwt.MapClaims)["iss"] {
				t.Error("issuer mismatch")
			}
		})
	}
}

func Test_sessionIDFromJwxToken(t *testing.T) {
	validIssuer := uuid.New()
	validSubject := uuid.New()
	validJTI := uuid.New()

	tests := []struct {
		name      string
		token     func() jwx.Token
		wantError bool
	}{
		{
			name: "valid token",
			token: func() jwx.Token {
				tok, _ := jwx.NewBuilder().
					Issuer(validIssuer.String()).
					Subject(validSubject.String()).
					JwtID(validJTI.String()).
					Build()
				return tok
			},
			wantError: false,
		},
		{
			name: "invalid issuer",
			token: func() jwx.Token {
				tok, _ := jwx.NewBuilder().
					Issuer("invalid").
					Subject(validSubject.String()).
					JwtID(validJTI.String()).
					Build()
				return tok
			},
			wantError: true,
		},
		{
			name: "invalid subject",
			token: func() jwx.Token {
				tok, _ := jwx.NewBuilder().
					Issuer(validIssuer.String()).
					Subject("invalid").
					JwtID(validJTI.String()).
					Build()
				return tok
			},
			wantError: true,
		},
		{
			name: "invalid jti",
			token: func() jwx.Token {
				tok, _ := jwx.NewBuilder().
					Issuer(validIssuer.String()).
					Subject(validSubject.String()).
					JwtID("invalid").
					Build()
				return tok
			},
			wantError: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			token := tt.token()

			sid, err := sessionIDFromJwxToken(token)

			if tt.wantError {
				if err == nil {
					t.Fatal("expected error, got nil")
				}
				return
			}

			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}

			if sid.issuerUUID.String() != validIssuer.String() {
				t.Error("issuer mismatch")
			}
		})
	}
}

func Test_sessionID_toModelPrimaryKey(t *testing.T) {
	sid := sessionID{
		issuerUUID:     uuid.New(),
		userNameUUID:   uuid.New(),
		sessionJTIUUID: uuid.New(),
	}

	pk := sid.toModelPrimaryKey()

	if !pk.iss.Valid || pk.iss.Bytes != sid.issuerUUID {
		t.Error("invalid iss")
	}
	if !pk.jti.Valid || pk.jti.Bytes != sid.sessionJTIUUID {
		t.Error("invalid jti")
	}
	if !pk.sub.Valid || pk.sub.Bytes != sid.userNameUUID {
		t.Error("invalid sub")
	}
}
