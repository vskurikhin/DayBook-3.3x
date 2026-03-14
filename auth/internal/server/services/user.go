package services

import (
	"github.com/jackc/pgx/v5/pgtype"
	"time"

	"github.com/google/uuid"

	"github.com/vskurikhin/DayBook-3.3x/auth/v2/internal/server/dto"
	"github.com/vskurikhin/DayBook-3.3x/auth/v2/internal/server/repository/user_name"
)

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

func CreatedUserFromModel(user user_name.UserName) CreatedUser {
	return CreatedUser{
		id:       user.ID.Bytes,
		userName: user.UserName,
		visible:  user.Visible,
		flags:    user.Flags,
	}
}

type User struct {
	userName string
	password string
	visible  bool
	flags    int32
}

func (u User) ToModel() user_name.CreateUserNameParams {
	return user_name.CreateUserNameParams{
		UserName: u.userName,
		Password: u.password,
	}
}

func (u User) UserNamePgTypeText() pgtype.Text {
	return pgtype.Text{
		Valid:  true,
		String: u.userName,
	}
}

func UserFromDto(user dto.Login) User {
	return User{
		userName: user.UserName,
		password: user.Password,
		visible:  user.Visible,
		flags:    user.Flags,
	}
}
