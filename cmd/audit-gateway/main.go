package main

import (
	"fmt"
	"log"

	"github.com/your-company/new-api-gateway/internal/config"
)

func main() {
	cfg, err := config.LoadFromEnv()
	if err != nil {
		log.Fatalf("configuration error: %v", err)
	}
	fmt.Printf("audit gateway core is configured for %s on %s; dependency wiring follows in the next task\n", cfg.NewAPIBaseURL, cfg.ListenAddr)
}
