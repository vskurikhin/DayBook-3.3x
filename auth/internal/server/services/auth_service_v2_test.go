package services

import (
	"context"
	"errors"
	"testing"

	"github.com/jackc/pgx/v5/pgtype"
	"github.com/stretchr/testify/assert"
	"go.uber.org/mock/gomock"

	"github.com/vskurikhin/DayBook-3.3x/auth/v2/internal/server/config"
	"github.com/vskurikhin/DayBook-3.3x/auth/v2/internal/server/repository/user_view"
)

func TestAuthServiceImplV2_auth(t *testing.T) {
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

			s := &AuthServiceImplV2{
				BaseService:  &BaseService{cfg: cfg},
				userViewRepo: mockUserViewRepo,
			}

			_, err := s.auth(context.Background(), tt.login)
			assert.Equal(t, tt.wantErr, err != nil)
		})
	}
}
