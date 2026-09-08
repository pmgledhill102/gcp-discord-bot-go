package discordbot

import (
	"context"
	"log"
	"net/http"
	"sync"

	"github.com/GoogleCloudPlatform/functions-framework-go/functions"
)

// This file is the deployable half of the package: it adapts the library in
// discordbot.go to the Google Cloud functions framework.
//
// The registration lives in the root package on purpose. Cloud Functions is
// deployed here with `--entry-point handleDiscordMessage`, and the Go buildpack
// looks for that registration in the module's root package, so moving it would
// break the documented deploy recipe.

// init registers the function.
//
// It deliberately does no configuration work. init() runs in every program that
// imports this package, so reading the environment here -- let alone calling
// log.Fatalf when a variable is absent, which is what this package used to do
// -- would make merely importing the library kill an unrelated process. The
// registration itself is inert.
func init() {
	functions.HTTP("handleDiscordMessage", serveFromEnv)
}

var (
	envOnce    sync.Once
	envHandler *Handler
	envErr     error
)

// HandlerFromEnv returns a [Handler] configured from the environment, building
// it at most once per process and returning the same one thereafter.
//
// The deployable calls this at startup so that a misconfigured revision fails
// before it can serve; see cmd/main.go.
func HandlerFromEnv(ctx context.Context) (*Handler, error) {
	envOnce.Do(func() {
		cfg, err := ConfigFromEnv()
		if err != nil {
			envErr = err
			return
		}

		envHandler, envErr = New(ctx, cfg)
	})

	return envHandler, envErr
}

// serveFromEnv is the functions-framework entry point.
//
// When the framework provides the process's main -- the Cloud Functions and
// `gcloud run deploy --function` routes -- nothing has had a chance to
// configure the handler beforehand, so it is built on the first request. A
// configuration error becomes a 503 rather than a crash, and is logged on every
// attempt so the cause is visible rather than buried in one startup line.
func serveFromEnv(w http.ResponseWriter, r *http.Request) {
	h, err := HandlerFromEnv(r.Context())
	if err != nil {
		log.Printf("Handler is not configured: %v", err)
		http.Error(w, "service is not configured", http.StatusServiceUnavailable)
		return
	}

	h.ServeHTTP(w, r)
}
