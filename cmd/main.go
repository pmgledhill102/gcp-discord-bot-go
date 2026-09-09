package main

import (
	"context"
	"log"
	"os"

	"github.com/GoogleCloudPlatform/functions-framework-go/funcframework"

	discordbot "github.com/pmgledhill102/gcp-discord-bot-go"
)

func main() {
	ctx := context.Background()

	// Build the handler before serving, so a misconfigured revision fails to
	// start rather than accepting traffic and failing every request. The
	// framework path shares this handler -- HandlerFromEnv builds at most one
	// per process -- so this costs nothing beyond moving the error earlier.
	//
	// log.Fatalf is correct here and only here: this is the deployable's
	// startup, not a request. Nothing on the request path may exit (#49).
	h, err := discordbot.HandlerFromEnv(ctx)
	if err != nil {
		log.Fatalf("Configuration error: %v", err)
	}
	defer func() {
		if err := h.Close(); err != nil {
			log.Printf("Error closing handler: %v", err)
		}
	}()

	// Use PORT environment variable, or default to 8080.
	port := "8080"
	if envPort := os.Getenv("PORT"); envPort != "" {
		port = envPort
	}

	// By default, listen on all interfaces. If testing locally, run with
	// LOCAL_ONLY=true to avoid triggering firewall warnings and
	// exposing the server outside of your own machine.
	hostname := ""
	if localOnly := os.Getenv("LOCAL_ONLY"); localOnly == "true" {
		hostname = "127.0.0.1"
	}
	if err := funcframework.StartHostPort(hostname, port); err != nil {
		log.Fatalf("funcframework.StartHostPort: %v\n", err)
	}
}
