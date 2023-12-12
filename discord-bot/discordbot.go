// Copyright 2023 Google LLC
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//      http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

package discordbot

import (
	"context"
	"encoding/hex"
	"fmt"
	"log"
	"net/http"

	"cloud.google.com/go/pubsub"
	"github.com/GoogleCloudPlatform/functions-framework-go/functions"
	"github.com/tink-crypto/tink-go/v2/signature/subtle"
)

const (
	publicKeyHex = "188e162d74e3f38533d4a26438a21f7850d349f81fef5fdc3c2dbab4c8702500"
	//const privateKey = "68457418cbb62af71afccc636b4f04c7128ddb39f654a2d77e352a9666c3c5f8188e162d74e3f38533d4a26438a21f7850d349f81fef5fdc3c2dbab4c8702500";
	signatureHex = "280f9fa9b27e4d755c3abfe3acff2cb8e4948f185a0608e9c1621b7c9f8f373e3e6716423d8f8969fe82999076e13390fff2be57b6f07299d55c12f9c3fbf70d"
	content      = "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"
	projectID    = "play-pen-pup"
	topicName    = "game-server-operations"
)

var sigVerifier *subtle.ED25519Verifier
var pubsubClient *pubsub.Client
var pubsubTopic *pubsub.Topic
var ctx context.Context

// Try and do things early. This won't help cold start - but should speed up warm queries
func init() {
	// Functions Framework
	functions.HTTP("handleDiscordMessage", handleDiscordMessage)

	// Signature validator
	publicKey, _ := hex.DecodeString(publicKeyHex)

	// Create an ED25519 verifier
	var err error
	sigVerifier, err = subtle.NewED25519Verifier(publicKey)
	if err != nil {
		log.Fatal("Error creating ED25519 verifier:", err)
	}
	// Create a Pub/Sub client.
	ctx = context.Background()
	pubsubClient, err = pubsub.NewClient(ctx, projectID)
	if err != nil {
		log.Fatalf("Failed to create client: %v", err)
	}

	// Get a Pub/Sub topic.
	pubsubTopic = pubsubClient.Topic(topicName)

}

// handleDiscordMessage is an HTTP Cloud Function.
func handleDiscordMessage(w http.ResponseWriter, r *http.Request) {

	// Convert strings to bytes
	signatureBytes, _ := hex.DecodeString(signatureHex)
	contentBytes := []byte(content)

	// Verify the signature
	if err := sigVerifier.Verify(signatureBytes, contentBytes); err != nil {
		log.Fatal("Signature verification failed:", err)
	}

	// Publish a message to the topic.
	result := pubsubTopic.Publish(ctx, &pubsub.Message{
		Data: []byte("Hello, World!"),
	})

	// Wait for the message to be published.
	id, err := result.Get(ctx)
	if err != nil {
		log.Fatalf("Failed to publish message: %v", err)
	}

	// Return
	fmt.Fprintf(w, "Hello! the function verified %s, msgId=%s", "true", id)
}
