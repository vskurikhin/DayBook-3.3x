package main

import (
	"context"
	"os"

	"github.com/vskurikhin/DayBook-3.3x/auth/v2/cmd/server/cmd"
)

func main() {
	os.Exit(cmd.Execute(context.Background()))
}
