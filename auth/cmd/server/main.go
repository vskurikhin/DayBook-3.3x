package main

import (
	"context"
	"github.com/vskurikhin/DayBook-3.3x/auth/v2/cmd/server/cmd"
	"os"
)

func main() {
	os.Exit(cmd.Execute(context.Background()))
}
