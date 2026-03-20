package services

import (
	"bytes"
	"encoding/json"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/vskurikhin/DayBook-3.3x/auth/v2/internal/server/dto"
	"github.com/vskurikhin/DayBook-3.3x/auth/v2/internal/server/repository/user_attrs"
	"github.com/vskurikhin/DayBook-3.3x/auth/v2/internal/server/repository/user_name"
	"github.com/vskurikhin/DayBook-3.3x/auth/v2/internal/server/repository/user_view"
)

type CreateUser struct {
	id              pgtype.UUID
	confirmPassword string
	email           string
	name            string
	password        string
	userName        string
}

func (u CreateUser) ToModelCreateUserNameParams() user_name.CreateUserNameParams {
	return user_name.CreateUserNameParams{
		Password: u.password,
		UserName: u.userName,
	}
}

func (u CreateUser) ToModelCreateUserAttrsParams() user_attrs.CreateUserAttrsParams {
	a := userAttrs{
		ID:       u.id.Bytes,
		Email:    u.email,
		Name:     u.name,
		UserName: u.userName,
	}
	var b bytes.Buffer
	encoder := json.NewEncoder(&b)
	_ = encoder.Encode(a)
	return user_attrs.CreateUserAttrsParams{
		Attrs:    b.Bytes(),
		Name:     u.name,
		UserName: u.userName,
	}
}

func CreateUserFromDto(user dto.CreateUser) CreateUser {
	return CreateUser{
		confirmPassword: user.ConfirmPassword,
		email:           user.Email,
		name:            user.Name,
		password:        user.Password,
		userName:        user.UserName,
	}
}

type CreatedUser struct {
	id       uuid.UUID
	userName string
	created  time.Time
	visible  bool
	flags    int32
}

func (u CreatedUser) ToDto() dto.CreatedUser {
	return dto.CreatedUser{
		ID:        u.id,
		UserName:  u.userName,
		CreatedAt: u.created,
		Visible:   u.visible,
		Flags:     u.flags,
	}
}

type Login struct {
	userName string
	password string
	visible  bool
	flags    int32
}

func (l Login) UserNamePgTypeText() pgtype.Text {
	return pgtype.Text{
		Valid:  true,
		String: l.userName,
	}
}

func LoginFromDto(login dto.Login) Login {
	return Login{
		userName: login.UserName,
		password: login.Password,
		visible:  login.Visible,
		flags:    login.Flags,
	}
}

type User struct {
	id       uuid.UUID
	name     string
	email    string
	userName string
	password string
	visible  bool
	flags    int32
}

func (u User) ToDto() dto.User {
	return dto.User{
		ID:       u.id,
		Name:     u.name,
		Email:    u.email,
		UserName: u.userName,
		Visible:  u.visible,
		Flags:    u.flags,
	}
}

func UserFromModelUserView(user user_view.UserView) User {
	var flags int32
	if user.Flags.Valid {
		flags = user.Flags.Int32
	}
	bb := bytes.NewBuffer(user.Attrs)
	decoder := json.NewDecoder(bb)
	var a userAttrs
	_ = decoder.Decode(&a)
	return User{
		id:       user.ID.Bytes,
		name:     user.Name.String,
		email:    a.Email,
		userName: user.UserName.String,
		visible:  user.Visible.Valid && user.Visible.Bool,
		flags:    flags,
	}
}

func UserFromModelUserName(user user_name.UserName) User {
	return User{
		id:       user.ID.Bytes,
		userName: user.UserName,
		visible:  user.Visible,
		flags:    user.Flags,
	}
}

func UserFromModelUserAttr(user user_attrs.UserAttr) User {
	bb := bytes.NewBuffer(user.Attrs)
	decoder := json.NewDecoder(bb)
	var a userAttrs
	_ = decoder.Decode(&a)
	return User{
		id:       a.ID,
		name:     a.Name,
		userName: a.UserName,
		email:    a.Email,
		visible:  user.Visible,
		flags:    user.Flags,
	}
}

type userAttrs struct {
	ID       uuid.UUID `json:"id"`
	Email    string    `json:"email"`
	Name     string    `json:"name"`
	UserName string    `json:"user_name"`
}
