// Command server boots the ADAS cloud API described in docs/design.md.
package main

import (
	"log"
	"net/http"

	"github.com/AbdurDRahman/RetroAdas/adas-cloud/config"
	"github.com/AbdurDRahman/RetroAdas/adas-cloud/database"
)

func main() {
	configuration, err := config.Load()
	if err != nil {
		log.Fatalf("config error: %v", err)
	}

	connectionPool, err := database.Connect(configuration.DatabaseURL)
	if err != nil {
		log.Fatalf("database connection error: %v", err)
	}
	defer connectionPool.Close()

	router := http.NewServeMux()

	// TODO(C-105): wire up the endpoints listed in docs/design.md section 8
	// once their underlying packages are green under TDD, starting with:
	//   POST /v1/devices/handshake -> internal/handshake
	//   POST /v1/hazards           -> internal/hazards.ProcessReport
	//   GET  /v1/hazards           -> internal/hazards.FindNearby
	// Each handler authenticates via internal/devices.Authenticate first.

	log.Printf("listening on :%s", configuration.ServerPort)
	log.Fatal(http.ListenAndServe(":"+configuration.ServerPort, router))
}
