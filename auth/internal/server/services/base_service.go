package services

import (
	"github.com/vskurikhin/DayBook-3.3x/auth/v2/internal/server/config"
	"github.com/vskurikhin/DayBook-3.3x/auth/v2/internal/server/db"
	"github.com/vskurikhin/DayBook-3.3x/auth/v2/internal/server/repository/session"
	"github.com/vskurikhin/DayBook-3.3x/auth/v2/internal/server/repository/user_attrs"
	"github.com/vskurikhin/DayBook-3.3x/auth/v2/internal/server/repository/user_name"
	"github.com/vskurikhin/DayBook-3.3x/auth/v2/internal/server/repository/user_view"
)

type BaseService interface {
}

var _ BaseService = (*AuthBaseService)(nil)

type AuthBaseService struct {
}

func NewAuthBaseService(
	_ config.Config,
	_ db.DB,
	_ session.Repo,
	_ user_attrs.Repo,
	_ user_name.Repo,
	_ user_view.Repo,
) *AuthBaseService {
	return &AuthBaseService{}
}
