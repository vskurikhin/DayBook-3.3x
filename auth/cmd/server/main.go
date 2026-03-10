package main

import (
	"context"
	"os"

	"github.com/vskurikhin/DayBook-3.3x/auth/v2/cmd/server/cmd"
)

// @title			DayBook Auth API
// @version			0.0.1
// @description		API for authentication in DayBook

// @host			localhost:8089
// @BasePath		/auth/api
// @schemes			http
func main() {
	os.Exit(cmd.Execute(context.Background()))
}
