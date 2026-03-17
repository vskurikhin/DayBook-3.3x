package services

import (
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"net/http"
	"os"
	"time"

	"github.com/golang-jwt/jwt/v5"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/vskurikhin/DayBook-3.3x/auth/v2/internal/server/config"
	"github.com/vskurikhin/DayBook-3.3x/auth/v2/internal/server/db"
	"github.com/vskurikhin/DayBook-3.3x/auth/v2/internal/server/repository/user_attrs"
	"github.com/vskurikhin/DayBook-3.3x/auth/v2/internal/server/repository/user_name"
	"github.com/vskurikhin/DayBook-3.3x/auth/v2/internal/server/repository/user_view"
	"github.com/vskurikhin/DayBook-3.3x/auth/v2/internal/server/xerror"
	"github.com/vskurikhin/DayBook-3.3x/auth/v2/pkg/tool"
)

type BaseService interface {
	Auth(ctx context.Context, login Login) (Creds, error)
	Refresh(ctx context.Context, token string) (Creds, error)
	Register(ctx context.Context, user CreateUser) (Creds, error)
}

type UserAttrsRepo interface {
	CreateUserAttrs(ctx context.Context, arg user_attrs.CreateUserAttrsParams) (user_attrs.UserAttr, error)
	WithTx(tx pgx.Tx) *user_attrs.Queries
}

type UserNameRepo interface {
	CreateUserName(ctx context.Context, arg user_name.CreateUserNameParams) (user_name.UserName, error)
	WithTx(tx pgx.Tx) *user_name.Queries
}

type UserViewRepo interface {
	GetUserName(ctx context.Context, userName pgtype.Text) (user_view.UserView, error)
}

var _ BaseService = (*AuthBaseService)(nil)

type AuthBaseService struct {
	cfg           config.Config
	dbPool        db.DB
	userAttrsRepo UserAttrsRepo
	userNameRepo  UserNameRepo
	userViewRepo  UserViewRepo
}

const hostname = "server0.v1.svn.su" // TODO convert to business logic ISSUE #

var (
	ErrInvalidPassword  = errors.New(xerror.InvalidPasswordError)
	ErrPasswordNotValid = errors.New(xerror.PasswordNotValidError)
	ErrLogout           = errors.New(xerror.Logout)
)

// TODO Не храните в исходниках кода!
// HMAC-секрет нужно хранить в надёжном месте, например, в хранилищах секретов (HashiCorp Vault, AWS Secrets Manager, Google Secret Manager, …).
var secret = []byte("your-secret-string")

func (s *AuthBaseService) Auth(ctx context.Context, login Login) (Creds, error) {
	view, errGetUserName := s.userViewRepo.GetUserName(ctx, login.UserNamePgTypeText())
	if errGetUserName != nil {
		return Creds{}, errGetUserName
	}
	if !view.Password.Valid {
		return Creds{}, ErrPasswordNotValid
	}
	if !tool.Verify(view.Password.String, login.password) {
		return Creds{}, ErrInvalidPassword
	}
	issuerUUID := uuid.NewSHA1(uuid.NameSpaceDNS, []byte(hostname))
	userNameUUID := uuid.NewSHA1(uuid.NameSpaceOID, []byte(view.UserName.String))
	sessionJTIUUID, err := uuid.NewV7()
	if err != nil {
		return Creds{}, err
	}
	accessClaims := jwt.MapClaims{
		"exp": time.Now().Add(time.Minute * 120).Unix(),
		"iss": issuerUUID,
		"jti": sessionJTIUUID,
		"sub": userNameUUID,
	}
	accessToken := jwt.NewWithClaims(jwt.SigningMethodHS256, accessClaims)
	signedAccessToken, err := accessToken.SignedString(secret)
	if err != nil {
		return Creds{}, err
	}
	refreshClaims := jwt.MapClaims{
		"exp": time.Now().Add(time.Hour * 24).Unix(),
		"iss": base64.StdEncoding.EncodeToString(issuerUUID[:]),
		"jti": base64.StdEncoding.EncodeToString(sessionJTIUUID[:]),
		"sub": base64.StdEncoding.EncodeToString(userNameUUID[:]),
	}
	refreshToken := jwt.NewWithClaims(jwt.SigningMethodHS256, refreshClaims)
	signedRefreshToken, err := refreshToken.SignedString(secret)
	if err != nil {
		return Creds{}, err
	}
	// Define the cookie
	cookie := http.Cookie{
		Name:     "refresh",
		Value:    signedRefreshToken,             // The cookie's value
		Path:     "/",                            // The path for which the cookie is valid
		Expires:  time.Now().Add(24 * time.Hour), // Set the expiration time
		HttpOnly: true,                           // Prevents JavaScript from accessing the cookie (security best practice)
		Secure:   true,                           // Ensures the cookie is only sent over HTTPS (security best practice)
		SameSite: http.SameSiteStrictMode,        // Mitigates CSRF attacks
	}
	u := UserFromModelUserView(view)
	return Creds{
		accessToken: Token{
			jwt:       signedAccessToken,
			expiresAt: time.Now().Add(time.Second * 5),
			user:      u,
		},
		refreshTokenCookie: cookie,
	}, nil
}

type CustomClaims struct {
	Jti string `json:"jti"` // Map the "jti" claim to a Go field
	jwt.RegisteredClaims
}

func (s *AuthBaseService) Refresh(ctx context.Context, token string) (Creds, error) {
	t, errJWTParse := jwt.ParseWithClaims(token, &CustomClaims{}, func(token *jwt.Token) (interface{}, error) {
		if _, ok := token.Method.(*jwt.SigningMethodHMAC); !ok {
			return nil, fmt.Errorf("unexpected signing method: %v", token.Header["alg"])
		}
		return secret, nil
	})
	if errJWTParse != nil {
		return Creds{}, errJWTParse
	}
	issuerUUID, errDecodeIssuer := CanClaimUUID(t.Claims.GetIssuer())
	if errDecodeIssuer != nil {
		return Creds{}, errDecodeIssuer
	}
	userNameUUID, err := CanClaimUUID(t.Claims.GetSubject())
	if err != nil {
		return Creds{}, err
	}
	var sessionJTIUUID uuid.UUID
	if claims, ok := t.Claims.(*CustomClaims); ok {
		sessionJTIUUID, err = CanClaimUUID(claims.Jti, nil)
		if err != nil {
			return Creds{}, err
		}
	}
	fmt.Fprintf(os.Stderr, "token: %v\nissuer: %s\nsubject: %s\njti: %s\n", t, issuerUUID.String(), userNameUUID.String(), sessionJTIUUID.String())

	return Creds{}, ErrLogout
}

func CanClaimUUID(s string, err error) (uuid.UUID, error) {
	if err != nil {
		return uuid.Nil, err
	}
	buf, errDecode := base64.StdEncoding.DecodeString(s)
	if errDecode != nil {
		return uuid.Nil, errDecode
	}
	var array [16]byte
	copy(array[:], buf)
	return array, nil
}

func (s *AuthBaseService) Register(ctx context.Context, user CreateUser) (Creds, error) {
	t := user
	var errHash error
	t.password, errHash = tool.Hash(user.password, int(s.cfg.Values().AuthCost))
	if errHash != nil {
		return Creds{}, errHash
	}
	tx, errBeginTx := s.dbPool.Begin(ctx)
	if errBeginTx != nil {
		return Creds{}, errBeginTx
	}
	userAttrsRepoTx := s.userAttrsRepo.WithTx(tx)
	userNameRepoTx := s.userNameRepo.WithTx(tx)
	un, errCreateUserName := userNameRepoTx.CreateUserName(ctx, t.ToModelCreateUserNameParams())
	if errCreateUserName != nil {
		_ = tx.Rollback(ctx)
		return Creds{}, errCreateUserName
	}
	_, errCreateUserAttrs := userAttrsRepoTx.CreateUserAttrs(ctx, t.ToModelCreateUserAttrsParams())
	if errCreateUserAttrs != nil {
		_ = tx.Rollback(ctx)
		return Creds{}, errCreateUserAttrs
	}
	err := tx.Commit(ctx)
	if err != nil {
		return Creds{}, err
	}
	issuerUUID := uuid.NewSHA1(uuid.NameSpaceDNS, []byte(hostname))
	userNameUUID := uuid.NewSHA1(uuid.NameSpaceOID, []byte(un.UserName))
	sessionJTIUUID, err := uuid.NewV7()
	if err != nil {
		return Creds{}, err
	}
	accessClaims := jwt.MapClaims{
		"exp": time.Now().Add(time.Minute * 120).Unix(),
		"iss": issuerUUID,
		"jti": sessionJTIUUID,
		"sub": userNameUUID,
	}
	accessToken := jwt.NewWithClaims(jwt.SigningMethodHS256, accessClaims)
	signedAccessToken, err := accessToken.SignedString(secret)
	if err != nil {
		return Creds{}, err
	}
	refreshClaims := jwt.MapClaims{
		"exp": time.Now().Add(time.Hour * 24).Unix(),
		"iss": base64.StdEncoding.EncodeToString(issuerUUID[:]),
		"jti": base64.StdEncoding.EncodeToString(sessionJTIUUID[:]),
		"sub": base64.StdEncoding.EncodeToString(userNameUUID[:]),
	}
	refreshToken := jwt.NewWithClaims(jwt.SigningMethodHS256, refreshClaims)
	signedRefreshToken, err := refreshToken.SignedString(secret)
	if err != nil {
		return Creds{}, err
	}
	// Define the cookie
	cookie := http.Cookie{
		Name:     "refresh",
		Value:    signedRefreshToken,             // The cookie's value
		Path:     "/",                            // The path for which the cookie is valid
		Expires:  time.Now().Add(24 * time.Hour), // Set the expiration time
		HttpOnly: true,                           // Prevents JavaScript from accessing the cookie (security best practice)
		Secure:   true,                           // Ensures the cookie is only sent over HTTPS (security best practice)
		SameSite: http.SameSiteStrictMode,        // Mitigates CSRF attacks
	}
	u := UserFromModelUserName(un)
	u.name = user.name
	u.email = user.email
	return Creds{
		accessToken: Token{
			jwt:       signedAccessToken,
			expiresAt: time.Now().Add(time.Minute * 5),
			user:      u,
		},
		refreshTokenCookie: cookie,
	}, nil
}

func NewAuthBaseService(
	cfg config.Config,
	dbPool db.DB,
	userAttrsRepo user_attrs.Repo,
	userNameRepo user_name.Repo,
	userViewRepo user_view.Repo,
) *AuthBaseService {
	return &AuthBaseService{
		cfg:           cfg,
		dbPool:        dbPool,
		userAttrsRepo: userAttrsRepo,
		userNameRepo:  userNameRepo,
		userViewRepo:  userViewRepo,
	}
}
