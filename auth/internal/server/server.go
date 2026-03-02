package server

import (
	"fmt"
	"log"
	"net/http"

	"github.com/vskurikhin/DayBook-3.3x/auth/v2/internal/server/config"
)

//go:generate mockgen -destination=../../mocks/server/mocks/server.go -package=mocks github.com/vskurikhin/DayBook-3.3x/auth/v2/internal/server Server
type Server interface {
	Run()
}

var _ Server = (*AuthServer)(nil)

type AuthServer struct {
	values config.Values
}

func NewAuthServer(cfg config.Config) *AuthServer {
	return &AuthServer{values: cfg.Values()}
}

func (a AuthServer) Run() {
	// Register the handler function for a specific route pattern
	http.HandleFunc("/", helloHandler)

	// Start the HTTP server on port 8080
	fmt.Printf("Server starting on %s...\n", a.values.Address)
	if err := http.ListenAndServe(a.values.Address, nil); err != nil {
		log.Fatal(err)
	}
}

func helloHandler(w http.ResponseWriter, r *http.Request) {
	// w is used to write the response
	// r contains the incoming request data
	_, _ = fmt.Fprintf(w, "Hello, World! You requested: %s\n", r.URL.Path)
}
