package services

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/go-chi/jwtauth/v5"
	"github.com/golang-jwt/jwt/v5"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"
	jwx "github.com/lestrrat-go/jwx/v3/jwt"
	"github.com/stretchr/testify/assert"
	"go.uber.org/mock/gomock"

	"github.com/vskurikhin/DayBook-3.3x/auth/v2/internal/server/config"
	"github.com/vskurikhin/DayBook-3.3x/auth/v2/internal/server/repository/session"
	"github.com/vskurikhin/DayBook-3.3x/auth/v2/internal/server/repository/user_attrs"
	"github.com/vskurikhin/DayBook-3.3x/auth/v2/internal/server/repository/user_view"
)

func TestServiceV2_List(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()

	mockUserAttrsRepo := NewMockUserAttrsRepo(ctrl)

	tests := []struct {
		name    string
		mock    func()
		wantErr bool
	}{
		{
			name: "success",
			mock: func() {
				mockUserAttrsRepo.EXPECT().
					ListUserAttrs(gomock.Any()).
					Return([]user_attrs.UserAttr{
						{UserName: "test"},
					}, nil)
			},
			wantErr: false,
		},
		{
			name: "repo error",
			mock: func() {
				mockUserAttrsRepo.EXPECT().
					ListUserAttrs(gomock.Any()).
					Return(nil, errors.New("err"))
			},
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			tt.mock()

			s := &ServiceV2{
				userAttrsRepo: mockUserAttrsRepo,
			}

			_, err := s.List(context.Background())
			assert.Equal(t, tt.wantErr, err != nil)
		})
	}
}

func TestServiceV2_Logout(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()

	mockSessionRepo := NewMockSessionRepo(ctrl)

	tests := []struct {
		name    string
		ctx     context.Context
		mock    func()
		wantErr bool
	}{
		{
			name:    "no token in context",
			ctx:     context.Background(),
			mock:    func() {},
			wantErr: true,
		},
		{
			name: "delete session success",
			ctx: func() context.Context {
				token := newTestJWXToken()
				ctx := context.WithValue(context.Background(), jwtauth.TokenCtxKey, token)
				return ctx
			}(),
			mock: func() {
				mockSessionRepo.EXPECT().
					DeleteSession(gomock.Any(), gomock.Any()).
					Return(nil)
			},
			wantErr: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			tt.mock()

			s := &ServiceV2{
				sessionRepo: mockSessionRepo,
			}

			err := s.Logout(tt.ctx)
			assert.Equal(t, tt.wantErr, err != nil)
		})
	}
}

func TestServiceV2_Refresh(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()

	mockSessionRepo := NewMockSessionRepo(ctrl)
	mockUserAttrsRepo := NewMockUserAttrsRepo(ctrl)

	cfg := NewMockConfig(ctrl)
	cfg.EXPECT().Values().Return(config.Values{
		JWThs256SignKey:        []byte("secret"),
		ValidPeriodAccessToken: time.Minute,
	}).AnyTimes()

	tests := []struct {
		name    string
		token   string
		mock    func()
		wantErr bool
	}{
		{
			name:    "invalid token",
			token:   "bad-token",
			mock:    func() {},
			wantErr: true,
		},
		{
			name: "session not found",
			token: func() string {
				tk := jwt.New(jwt.SigningMethodHS256)
				s, _ := tk.SignedString([]byte("secret"))
				return s
			}(),
			mock: func() {
				mockSessionRepo.EXPECT().
					GetSession(gomock.Any(), gomock.Any()).
					Return(session.Session{}, errors.New("err"))
			},
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			tt.mock()

			s := &ServiceV2{
				ServiceV1:     &ServiceV1{BaseService: &BaseService{cfg: cfg}},
				sessionRepo:   mockSessionRepo,
				userAttrsRepo: mockUserAttrsRepo,
			}

			_, err := s.Refresh(context.Background(), tt.token)
			assert.Equal(t, tt.wantErr, err != nil)
		})
	}
}

func TestServiceV2_auth(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()

	mockUserViewRepo := NewMockUserViewRepo(ctrl)

	cfg := NewMockConfig(ctrl)
	cfg.EXPECT().Values().Return(config.Values{
		Hostname: "test",
	}).AnyTimes()

	tests := []struct {
		name    string
		login   Login
		mock    func()
		wantErr bool
	}{
		{
			name:  "user not found",
			login: Login{userName: "test", password: "pass"},
			mock: func() {
				mockUserViewRepo.EXPECT().
					GetUserName(gomock.Any(), gomock.Any()).
					Return(user_view.UserView{}, errors.New("err"))
			},
			wantErr: true,
		},
		{
			name:  "invalid password",
			login: Login{userName: "test", password: "wrong"},
			mock: func() {
				mockUserViewRepo.EXPECT().
					GetUserName(gomock.Any(), gomock.Any()).
					Return(user_view.UserView{
						Password: pgtype.Text{String: "hashed", Valid: true},
						UserName: pgtype.Text{String: "test", Valid: true},
					}, nil)
			},
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			tt.mock()

			s := &ServiceV2{
				ServiceV1:    &ServiceV1{BaseService: &BaseService{cfg: cfg}},
				userViewRepo: mockUserViewRepo,
			}

			_, err := s.auth(context.Background(), tt.login)
			assert.Equal(t, tt.wantErr, err != nil)
		})
	}
}

func TestServiceV2_refresh(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()

	mockSessionRepo := NewMockSessionRepo(ctrl)

	cfg := NewMockConfig(ctrl)
	cfg.EXPECT().Values().Return(config.Values{
		ValidPeriodAccessToken: time.Minute,
	}).AnyTimes()

	tests := []struct {
		name    string
		claims  jwt.Claims
		mock    func()
		wantErr bool
	}{
		{
			name:    "invalid claims",
			claims:  jwt.MapClaims{},
			mock:    func() {},
			wantErr: true,
		},
		{
			name: "session repo error",
			claims: jwt.MapClaims{
				"iss": "test",
				"sub": "test",
				"jti": "test",
			},
			mock: func() {
				mockSessionRepo.EXPECT().
					GetSession(gomock.Any(), gomock.Any()).
					Return(session.Session{}, errors.New("err")).
					AnyTimes()
			},
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			tt.mock()

			s := &ServiceV2{
				ServiceV1:   &ServiceV1{BaseService: &BaseService{cfg: cfg}},
				sessionRepo: mockSessionRepo,
			}

			_, err := s.refresh(context.Background(), tt.claims)
			assert.Equal(t, tt.wantErr, err != nil)
		})
	}
}

func newTestJWXToken() jwx.Token {
	token := jwx.New()
	_ = token.Set(jwx.IssuerKey, uuid.New().String())
	_ = token.Set(jwx.SubjectKey, uuid.New().String())
	_ = token.Set(jwx.JwtIDKey, uuid.New().String())
	return token
}
