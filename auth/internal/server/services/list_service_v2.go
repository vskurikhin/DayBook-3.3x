package services

import (
	"context"

	"github.com/vskurikhin/DayBook-3.3x/auth/v2/internal/server/config"
	"github.com/vskurikhin/DayBook-3.3x/auth/v2/internal/server/repository/user_attrs"
	"github.com/vskurikhin/DayBook-3.3x/auth/v2/internal/server/services/model"
	"github.com/vskurikhin/DayBook-3.3x/auth/v2/pkg/tool"
)

type ListServiceV2 interface {
	List(ctx context.Context) ([]model.User, error)
}

var _ ListServiceV2 = (*ListServiceImplV2)(nil)

type ListServiceImplV2 struct {
	*BaseService
	userAttrsRepo user_attrs.Repo
}

func (s *ListServiceImplV2) List(ctx context.Context) ([]model.User, error) {
	list, err := s.userAttrsRepo.ListUserAttrs(ctx)
	if err != nil {
		return nil, err
	}
	return tool.Map(list, model.UserFromModelUserAttr), err
}

func NewListServiceV2(
	cfg config.Config,
	userAttrsRepo user_attrs.Repo,
) *ListServiceImplV2 {
	return &ListServiceImplV2{
		BaseService: &BaseService{
			cfg: cfg,
		},
		userAttrsRepo: userAttrsRepo,
	}
}
