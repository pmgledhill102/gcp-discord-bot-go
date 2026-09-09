// Package discordbot implements a Discord interaction endpoint that verifies
// each request's Ed25519 signature, copies the interaction onto a Pub/Sub
// topic, and returns a deferred acknowledgement inside Discord's 3 second
// deadline.
//
// The [Handler] is an ordinary [http.Handler], so it composes with any router
// or middleware:
//
//	cfg := discordbot.Config{
//		PublicKey: os.Getenv(discordbot.EnvPublicKey),
//		ProjectID: "my-project",
//		TopicName: "discord-ops",
//	}
//
//	h, err := discordbot.New(ctx, cfg)
//	if err != nil {
//		return err
//	}
//	defer h.Close()
//
//	http.Handle("/interactions", h)
//
// Deploying this repository as a Cloud Function or Cloud Run service instead
// is covered by function.go and the README; that path configures itself from
// the environment.
package discordbot

import (
	"context"
	"crypto/ed25519"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"sort"
	"strings"

	"cloud.google.com/go/pubsub"
	"github.com/bwmarrin/discordgo"
)

// Environment variables read by [ConfigFromEnv].
const (
	EnvPublicKey = "PUBLIC_SIG_KEY"
	EnvProjectID = "PUBSUB_PROJECT_ID"
	EnvTopicName = "PUBSUB_TOPIC_NAME"
)

// Config is the configuration for a [Handler].
type Config struct {
	// PublicKey is the application's public key from the Discord developer
	// portal, hex encoded. It verifies signatures rather than creating them,
	// so it is not a secret.
	PublicKey string

	// ProjectID is the Google Cloud project owning the Pub/Sub topic.
	ProjectID string

	// TopicName is the Pub/Sub topic that interactions are published to. The
	// caller's credentials need roles/pubsub.publisher on it.
	TopicName string
}

// publicKey decodes and validates Config.PublicKey.
//
// The length check is not decoration: [ed25519.Verify] panics on a wrong-sized
// key, so without it a mistyped key becomes a panic on the first interaction
// rather than an error at construction.
func (c Config) publicKey() (ed25519.PublicKey, error) {
	raw, err := hex.DecodeString(c.PublicKey)
	if err != nil {
		return nil, fmt.Errorf("public key is not valid hex: %w", err)
	}

	if len(raw) != ed25519.PublicKeySize {
		return nil, fmt.Errorf("public key must decode to %d bytes, got %d",
			ed25519.PublicKeySize, len(raw))
	}

	return ed25519.PublicKey(raw), nil
}

// ConfigFromEnv builds a [Config] from the environment, naming every variable
// that is missing rather than only the first.
func ConfigFromEnv() (Config, error) {
	cfg := Config{
		PublicKey: os.Getenv(EnvPublicKey),
		ProjectID: os.Getenv(EnvProjectID),
		TopicName: os.Getenv(EnvTopicName),
	}

	var missing []string
	for name, value := range map[string]string{
		EnvPublicKey: cfg.PublicKey,
		EnvProjectID: cfg.ProjectID,
		EnvTopicName: cfg.TopicName,
	} {
		if value == "" {
			missing = append(missing, name)
		}
	}

	if len(missing) > 0 {
		sort.Strings(missing)
		return Config{}, fmt.Errorf("required environment variables not set: %s",
			strings.Join(missing, ", "))
	}

	return cfg, nil
}

// publisher is the narrow slice of Pub/Sub this package uses. It exists so the
// request path can be tested without a broker, and is unexported because no
// consumer has needed to substitute one; exporting it later is additive.
type publisher interface {
	Publish(ctx context.Context, data []byte) (string, error)
	Close() error
}

// topicPublisher publishes to a real Pub/Sub topic.
type topicPublisher struct {
	client *pubsub.Client
	topic  *pubsub.Topic
}

func (t *topicPublisher) Publish(ctx context.Context, data []byte) (string, error) {
	return t.topic.Publish(ctx, &pubsub.Message{Data: data}).Get(ctx)
}

func (t *topicPublisher) Close() error {
	t.topic.Stop()
	return t.client.Close()
}

// Handler verifies and forwards Discord interactions. Create one with [New].
type Handler struct {
	key ed25519.PublicKey
	pub publisher
}

// New validates cfg and connects to Pub/Sub.
//
// The returned Handler owns the Pub/Sub client it creates, so callers should
// [Handler.Close] it when finished.
func New(ctx context.Context, cfg Config) (*Handler, error) {
	key, err := cfg.publicKey()
	if err != nil {
		return nil, err
	}

	if cfg.ProjectID == "" {
		return nil, errors.New("ProjectID is required")
	}

	if cfg.TopicName == "" {
		return nil, errors.New("TopicName is required")
	}

	client, err := pubsub.NewClient(ctx, cfg.ProjectID)
	if err != nil {
		return nil, fmt.Errorf("creating Pub/Sub client: %w", err)
	}

	return &Handler{
		key: key,
		pub: &topicPublisher{client: client, topic: client.Topic(cfg.TopicName)},
	}, nil
}

// Close releases the Pub/Sub client.
func (h *Handler) Close() error {
	if h.pub == nil {
		return nil
	}

	return h.pub.Close()
}

// ServeHTTP handles one Discord interaction.
//
// Nothing on this path may call log.Fatalf, os.Exit or panic. The endpoint is
// necessarily reachable without authentication -- Discord cannot present a
// Google identity, so the signature check is the only thing in front of it --
// and exiting on bad input hands anyone a way to kill the process (#49).
func (h *Handler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	// Discord requires 401 when verification fails, and runs automated checks
	// that purposefully send invalid signatures to confirm it does, removing
	// the registered interactions URL if they are not rejected. This is a
	// conformance requirement, not only a robustness one.
	if !discordgo.VerifyInteraction(r, h.key) {
		http.Error(w, "invalid request signature", http.StatusUnauthorized)
		return
	}

	// VerifyInteraction restores r.Body after reading it.
	body, err := io.ReadAll(r.Body)
	if err != nil {
		log.Printf("Unable to read request body: %v", err)
		http.Error(w, "unable to read request body", http.StatusBadRequest)
		return
	}

	var interaction discordgo.Interaction
	if err := interaction.UnmarshalJSON(body); err != nil {
		log.Printf("Unable to parse interaction: %v", err)
		http.Error(w, "malformed interaction payload", http.StatusBadRequest)
		return
	}

	switch interaction.Type {
	case discordgo.InteractionPing:
		// Discord's endpoint validation handshake. No Pub/Sub round trip.
		respond(w, discordgo.InteractionResponsePong)
		return

	case discordgo.InteractionApplicationCommand:
		// Handled below.

	default:
		// Buttons, select menus, modal submissions and autocomplete all arrive
		// here correctly signed. Refusing them is fine; exiting is not.
		log.Printf("Unhandled interaction type %d", interaction.Type)
		http.Error(w, "unhandled interaction type", http.StatusBadRequest)
		return
	}

	// A Pub/Sub outage is a failed request, not a reason to take the process
	// down with it.
	if _, err := h.pub.Publish(r.Context(), body); err != nil {
		log.Printf("Failed to publish message: %v", err)
		http.Error(w, "failed to queue interaction", http.StatusInternalServerError)
		return
	}

	// Acknowledge within Discord's 3 second budget. Whatever consumes the topic
	// sends the real message afterwards, via a webhook.
	respond(w, discordgo.InteractionResponseDeferredChannelMessageWithSource)
}

// respond writes a bare interaction response of the given type.
func respond(w http.ResponseWriter, resType discordgo.InteractionResponseType) {
	body, err := json.Marshal(discordgo.InteractionResponse{Type: resType})
	if err != nil {
		log.Printf("Error marshalling response: %v", err)
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}

	w.Header().Add("Content-Type", "application/json")

	// The rule below guards against XSS from writing untrusted input into an
	// HTML response. Neither half applies: `body` is the output of json.Marshal
	// on a struct whose only field is an integer constant chosen by this
	// function, no request data reaches it, and the response is
	// application/json rather than HTML. html/template, which the rule
	// recommends, would produce an invalid interaction response.
	//
	// nosemgrep: go.lang.security.audit.xss.no-direct-write-to-responsewriter.no-direct-write-to-responsewriter
	if _, err := w.Write(body); err != nil {
		// The client is already gone; there is nothing left to send it.
		log.Printf("Error writing response: %v", err)
	}
}
