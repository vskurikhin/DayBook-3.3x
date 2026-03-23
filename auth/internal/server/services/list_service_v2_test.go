package services

import (
	"context"
	"errors"
	"testing"

	"github.com/stretchr/testify/assert"
	"go.uber.org/mock/gomock"

	"github.com/vskurikhin/DayBook-3.3x/auth/v2/internal/server/config"
	"github.com/vskurikhin/DayBook-3.3x/auth/v2/internal/server/repository/user_attrs"
)

func TestListServiceImplV2_List(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()

	mockCfg := NewMockConfig(ctrl)
	mockUserAttrsRepo := NewMockUserAttrsRepo(ctrl)

	tests := []struct {
		name    string
		mock    func()
		wantErr bool
	}{
		{
			name: "success",
			mock: func() {
				mockCfg.EXPECT().
					Values().
					Return(config.Values{}).
					Times(0)
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
				mockCfg.EXPECT().
					Values().
					Return(config.Values{}).
					Times(0)
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

			s := &ListServiceImplV2{
				BaseService: &BaseService{
					cfg: mockCfg,
				},
				userAttrsRepo: mockUserAttrsRepo,
			}

			_, err := s.List(context.Background())
			assert.Equal(t, tt.wantErr, err != nil)
		})
	}
}
