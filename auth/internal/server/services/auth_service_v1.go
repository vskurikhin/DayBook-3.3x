package services

import (
	"github.com/vskurikhin/DayBook-3.3x/auth/v2/internal/server/config"
	"github.com/vskurikhin/DayBook-3.3x/auth/v2/internal/server/db"
)

const Ok = "ok"

type AuthServiceV1 interface {
	Ok() string
}

var _ AuthServiceV1 = (*ServiceV1)(nil)

type ServiceV1 struct {
	*BaseService
	dbPool db.DB
}

func (s *ServiceV1) Ok() string {
	return Ok
}

func NewAuthServiceV1(cfg config.Config, dbPool db.DB) *ServiceV1 {
	return &ServiceV1{
		BaseService: &BaseService{
			cfg: cfg,
		},
		dbPool: dbPool,
	}
}
