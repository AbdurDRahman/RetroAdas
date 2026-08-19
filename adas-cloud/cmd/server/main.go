// Command server boots the ADAS cloud API described in api_design.md.
package main

import (
	"log"
	"net/http"

	"github.com/YOUR-ORG/adas-cloud/config"
	"github.com/YOUR-ORG/adas-cloud/database"
)

func main() {
	cfg, err := config.Load()
	if err != nil {
		log.Fatalf("config error: %v", err)
	}

	db, err := database.Connect(cfg.DatabaseURL)
	if err != nil {
		log.Fatalf("database connection error: %v", err)
	}
	defer db.Close()

	mux := http.NewServeMux()

	// TODO: wire up the three endpoints once their underlying packages are
	// green under TDD:
	//   POST /v1/devices/handshake -> internal/handshake
	//   POST /v1/hazards           -> internal/hazards.ProcessReport
	//   GET  /v1/hazards           -> internal/hazards.FindNearby
	// Each handler authenticates via internal/devices.Authenticate first.

	log.Printf("listening on :%s", cfg.ServerPort)
	log.Fatal(http.ListenAndServe(":"+cfg.ServerPort, mux))
}
