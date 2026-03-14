package repository

import (
	"context"
	"log"

	"github.com/vskurikhin/DayBook-3.3x/auth/v2/internal/server/db"
	"github.com/vskurikhin/DayBook-3.3x/auth/v2/internal/server/repository/user_name"
)

func probe() error {
	pool, err := db.GetDB()
	if err != nil {
		return err
	}
	queries := user_name.New(pool)
	// list all authors
	authors, err := queries.ListUserNames(context.Background())
	if err != nil {
		return err
	}
	log.Println(authors)

	return nil
}
