package discordbot

import (
	"context"
	"crypto/ed25519"
	"encoding/hex"
	"encoding/json"
	"io"
	"log"
	"net/http"
	"os"

	"cloud.google.com/go/pubsub"
	"github.com/GoogleCloudPlatform/functions-framework-go/functions"
	"github.com/bwmarrin/discordgo"
)

// Required Environment Variables
const (
	envSignatureKeyKey    = "PUBLIC_SIG_KEY"
	envPubSubProjectIdKey = "PUBSUB_PROJECT_ID"
	envPubSubTopicNameKey = "PUBSUB_TOPIC_NAME"
)

// Decoded once at startup rather than per request. The key is constant, so
// decoding it on every interaction is both wasted work and the reason a
// malformed key used to surface as a crash on the first request instead of as
// a failure to start.
var signatureKey ed25519.PublicKey

var pubSubProjectId string
var pubSubTopicName string

var pubsubClient *pubsub.Client
var pubsubTopic *pubsub.Topic
var ctx context.Context

// init()
//
// log.Fatalf is deliberate here and only here. Missing or malformed
// configuration is a deployment error: failing fast means the container never
// becomes ready and the bad revision never takes traffic. That is the opposite
// of the request path, where exiting turns untrusted input into a process kill.
func init() {
	// Read Environment Variables
	rawSignatureKey := os.Getenv(envSignatureKeyKey)
	pubSubProjectId = os.Getenv(envPubSubProjectIdKey)
	pubSubTopicName = os.Getenv(envPubSubTopicNameKey)

	if rawSignatureKey == "" || pubSubProjectId == "" || pubSubTopicName == "" {
		log.Fatalf("Error: Required Environment Variables not set %s, %s, %s",
			envSignatureKeyKey, envPubSubProjectIdKey, envPubSubTopicNameKey)
	}

	keyBytes, err := hex.DecodeString(rawSignatureKey)
	if err != nil {
		log.Fatalf("Error: %s is not valid hex: %v", envSignatureKeyKey, err)
	}

	// ed25519.Verify panics on a wrong-sized key, so a mistyped key would
	// otherwise be a second way for a request to take the process down.
	if len(keyBytes) != ed25519.PublicKeySize {
		log.Fatalf("Error: %s must decode to %d bytes, got %d",
			envSignatureKeyKey, ed25519.PublicKeySize, len(keyBytes))
	}
	signatureKey = ed25519.PublicKey(keyBytes)

	// Create a Pub/Sub client.
	ctx = context.Background()
	pubsubClient, err = pubsub.NewClient(ctx, pubSubProjectId)
	if err != nil {
		log.Fatalf("Failed to create Pub/Sub client: %v", err)
	}

	// Get a Pub/Sub topic.
	pubsubTopic = pubsubClient.Topic(pubSubTopicName)

	// Functions Framework
	functions.HTTP("handleDiscordMessage", handleDiscordMessage)
}

// handleDiscordMessage
//
// Nothing below may call log.Fatalf, os.Exit or panic. This handler is served
// on a publicly invokable endpoint -- Discord cannot present a Google identity,
// so the signature check is the only thing standing in front of it -- and
// exiting on bad input hands anyone on the internet a way to kill the instance.
func handleDiscordMessage(w http.ResponseWriter, r *http.Request) {
	// Discord requires 401 when verification fails, and runs automated checks
	// that send deliberately invalid signatures to confirm it does. Failing
	// those costs the registered interactions URL, so this is a conformance
	// requirement and not only a robustness one.
	if !discordgo.VerifyInteraction(r, signatureKey) {
		http.Error(w, "invalid request signature", http.StatusUnauthorized)
		return
	}

	// VerifyInteraction restores r.Body after reading it, so the payload is
	// still available here.
	bodyBytes, err := io.ReadAll(r.Body)
	if err != nil {
		log.Printf("Unable to read request body: %v", err)
		http.Error(w, "unable to read request body", http.StatusBadRequest)
		return
	}

	var interaction discordgo.Interaction
	if err := interaction.UnmarshalJSON(bodyBytes); err != nil {
		log.Printf("Unable to parse interaction: %v", err)
		http.Error(w, "malformed interaction payload", http.StatusBadRequest)
		return
	}

	switch interaction.Type {
	case discordgo.InteractionPing:
		// Handle a Ping - don't bother with a PubSub message
		respondBack(w, discordgo.InteractionResponsePong)
		return

	case discordgo.InteractionApplicationCommand:
		// Handled below.

	default:
		// Buttons, select menus, modal submissions and autocomplete all arrive
		// here, correctly signed. Refusing them is fine; exiting is not.
		log.Printf("Unhandled interaction type %d", interaction.Type)
		http.Error(w, "unhandled interaction type", http.StatusBadRequest)
		return
	}

	// Publish the request to the topic
	result := pubsubTopic.Publish(ctx, &pubsub.Message{
		Data: bodyBytes,
	})

	// Wait for the message to be published. A Pub/Sub outage is a bad gateway,
	// not a reason to take the instance down with it.
	if _, err := result.Get(ctx); err != nil {
		log.Printf("Failed to publish message: %v", err)
		http.Error(w, "failed to queue interaction", http.StatusInternalServerError)
		return
	}

	// Tell Discord to await a response from the job
	// Need to follow this up with a WebHook call with the actual message
	respondBack(w, discordgo.InteractionResponseDeferredChannelMessageWithSource)
}

// Helper function to respond to interaction
func respondBack(w http.ResponseWriter, resType discordgo.InteractionResponseType) {
	var res discordgo.InteractionResponse
	res.Type = resType

	// Marshal the object into a byte slice
	resBytes, err := json.Marshal(res)
	if err != nil {
		log.Printf("Error marshalling JSON into byte array: %v", err)
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}

	// Set the content type to be JSON
	w.Header().Add("Content-Type", "application/json")

	// Write out the response
	if _, err := w.Write(resBytes); err != nil {
		// The client is already gone; there is nothing left to send it.
		log.Printf("Error writing response: %v", err)
	}
}
