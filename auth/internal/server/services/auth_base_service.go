package services

import (
	"context"
	"errors"
	"time"

	"github.com/golang-jwt/jwt/v5"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/vskurikhin/DayBook-3.3x/auth/v2/internal/server/config"
	"github.com/vskurikhin/DayBook-3.3x/auth/v2/internal/server/repository/user_name"
	"github.com/vskurikhin/DayBook-3.3x/auth/v2/internal/server/repository/user_view"
	"github.com/vskurikhin/DayBook-3.3x/auth/v2/pkg/tool"
)

type BaseService interface {
	Auth(ctx context.Context, user User) (Token, error)
	Register(ctx context.Context, user User) (CreatedUser, error)
}

type UserNameRepo interface {
	CreateUserName(ctx context.Context, arg user_name.CreateUserNameParams) (user_name.UserName, error)
}

type UserViewRepo interface {
	GetUserName(ctx context.Context, userName pgtype.Text) (user_view.UserView, error)
}

var _ BaseService = (*AuthBaseService)(nil)

type AuthBaseService struct {
	cfg          config.Config
	userNameRepo UserNameRepo
	userViewRepo UserViewRepo
}

const issuer = "best.hotel.com"

func (s *AuthBaseService) Auth(ctx context.Context, user User) (Token, error) {
	view, errGetUserName := s.userViewRepo.GetUserName(ctx, user.UserNamePgTypeText())
	if errGetUserName != nil {
		return Token{}, errGetUserName
	}
	if !view.Password.Valid {
		return Token{}, errors.New("password not valid")
	}
	if !tool.Verify(view.Password.String, user.password) {
		return Token{}, errors.New("invalid password")
	}
	claims := jwt.MapClaims{
		"iss":       issuer,
		"iat":       time.Now().Unix(),
		"user_name": view.UserName,
	}
	token := jwt.NewWithClaims(jwt.SigningMethodHS256, claims)
	// Не храните в исходниках кода!
	// HMAC-секрет нужно хранить в надёжном месте, например, в хранилищах секретов (HashiCorp Vault, AWS Secrets Manager, Google Secret Manager, …).
	var secret = []byte("your-secret-string")
	signedToken, err := token.SignedString(secret)
	if err != nil {
		return Token{}, err
	}
	return Token{
		jwt: signedToken,
	}, nil
}

func (s *AuthBaseService) Register(ctx context.Context, user User) (CreatedUser, error) {
	t := user
	var errHash error
	t.password, errHash = tool.Hash(user.password, int(s.cfg.Values().AuthCost))
	if errHash != nil {
		return CreatedUser{}, errHash
	}
	u, err := s.userNameRepo.CreateUserName(ctx, t.ToModel())
	if err != nil {
		return CreatedUser{}, err
	}
	return CreatedUserFromModel(u), nil
}

func NewAuthBaseService(cfg config.Config, userNameRepo user_name.Repo, userViewRepo user_view.Repo) *AuthBaseService {
	return &AuthBaseService{
		cfg:          cfg,
		userNameRepo: userNameRepo,
		userViewRepo: userViewRepo,
	}
}
