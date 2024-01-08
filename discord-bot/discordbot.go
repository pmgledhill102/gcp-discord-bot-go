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

var signatureKey string
var pubSubProjectId string
var pubSubTopicName string

var pubsubClient *pubsub.Client
var pubsubTopic *pubsub.Topic
var ctx context.Context

// init()
func init() {
	// Read Environment Variables
	signatureKey = os.Getenv(envSignatureKeyKey)
	pubSubProjectId = os.Getenv(envPubSubProjectIdKey)
	pubSubTopicName = os.Getenv(envPubSubTopicNameKey)

	if signatureKey == "" || pubSubProjectId == "" || pubSubTopicName == "" {
		log.Fatalf("Error: Required Environment Variables not set %s, %s, %s",
			envSignatureKeyKey, envPubSubProjectIdKey, envPubSubTopicNameKey)
	}

	// Create a Pub/Sub client.
	ctx = context.Background()
	var err error
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
func handleDiscordMessage(w http.ResponseWriter, r *http.Request) {
	// Verify the message signature
	publicKeyBytes, err := hex.DecodeString(signatureKey)
	if err != nil {
		log.Fatalf("Unable to decode signature key %v", err)
	}

	if !discordgo.VerifyInteraction(r, ed25519.PublicKey(publicKeyBytes)) {
		log.Fatalf("Invalid Signature")
	}

	// Try and Unmarshal data into an object
	var interaction discordgo.Interaction
	bodyBytes, _ := io.ReadAll(r.Body)
	interaction.UnmarshalJSON(bodyBytes)

	// Handle a Ping - don't bother with a PubSub message
	if interaction.Type == discordgo.InteractionPing {
		respondBack(w, discordgo.InteractionResponsePong)
		return
	}

	// Disgard anything else other than an AppCommand
	if interaction.Type != discordgo.InteractionApplicationCommand {
		log.Fatalf("Can only handle interactions of type 'InteractionApplicationCommand' - Type is %d", interaction.Type)
	}

	// Publish the request to the topic
	result := pubsubTopic.Publish(ctx, &pubsub.Message{
		Data: bodyBytes,
	})

	// Wait for the message to be published.
	_, err = result.Get(ctx)
	if err != nil {
		log.Fatalf("Failed to publish message: %v", err)
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
		log.Fatalf("Error marshalling JSON into byte array: %v", err)
		panic(err)
	}

	// Set the content type to be JSON
	w.Header().Add("Content-Type", "application/json")

	// Write out the response
	w.Write(resBytes)
}
