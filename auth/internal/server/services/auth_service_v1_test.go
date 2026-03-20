package services

import (
	"testing"

	"github.com/stretchr/testify/require"
	"go.uber.org/mock/gomock"
)

func TestNewAuthServiceV1(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()

	cfg := NewMockConfig(ctrl) // можно использовать пустую конфигурацию

	svc := NewAuthServiceV1(cfg, nil)

	require.NotNil(t, svc)
	require.Equal(t, cfg, svc.cfg)
	require.Equal(t, nil, svc.dbPool)
	require.NotNil(t, svc.BaseService)
}

func TestServiceV1_Ok(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()
	cfg := NewMockConfig(ctrl) // можно использовать пустую конфигурацию

	svc := NewAuthServiceV1(cfg, nil)

	result := svc.Ok()
	require.Equal(t, Ok, result)
}
