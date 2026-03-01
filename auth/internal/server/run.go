package server

import (
	"fmt"
	"log"
	"net/http"

	"github.com/vskurikhin/DayBook-3.3x/auth/v2/internal/server/config"
)

var Run = run

func helloHandler(w http.ResponseWriter, r *http.Request) {
	// w is used to write the response
	// r contains the incoming request data
	_, _ = fmt.Fprintf(w, "Hello, World! You requested: %s\n", r.URL.Path)
}

func run(cfg config.Config) {
	// Register the handler function for a specific route pattern
	http.HandleFunc("/", helloHandler)

	// Start the HTTP server on port 8080
	fmt.Printf("Server starting on %s...\n", cfg.Address)
	if err := http.ListenAndServe(cfg.Address, nil); err != nil {
		log.Fatal(err)
	}
}
