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
	fmt.Printf("audit gateway configured for %s on %s\n", cfg.NewAPIBaseURL, cfg.ListenAddr)
}
