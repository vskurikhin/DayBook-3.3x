package dto

import (
	"github.com/google/uuid"
	"time"
)

type CreatedUser struct {
	ID        uuid.UUID `json:"id"`
	UserName  string    `json:"user_name"`
	CreatedAt time.Time `json:"created_at"`
	Visible   bool      `json:"visible,omitempty" swaggerignore:"true"`
	Flags     int32     `json:"flags,omitempty"  swaggerignore:"true"`
}

type Login struct {
	UserName string `json:"user_name"`
	Password string `json:"password"`
	Visible  bool   `json:"visible,omitempty" swaggerignore:"true"`
	Flags    int32  `json:"flags,omitempty"  swaggerignore:"true"`
}

type User struct {
	UserName string `json:"user_name"`
	Visible  bool   `json:"visible,omitempty" swaggerignore:"true"`
	Flags    int32  `json:"flags,omitempty"  swaggerignore:"true"`
}

func (l Login) ToUser() User {
	return User{
		UserName: l.UserName,
		Visible:  l.Visible,
		Flags:    l.Flags,
	}
}
