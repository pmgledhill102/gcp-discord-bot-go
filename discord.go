package discordbot

import (
	"bytes"
	"crypto/ed25519"
	"encoding/hex"
	"io"
	"net/http"
)

// As much of Discord's interaction protocol as this service needs, which is
// very little: one signature check, one integer field and four constants.
//
// This replaces github.com/bwmarrin/discordgo (#56). That is a complete Discord
// API client -- gateway, REST and voice -- and depending on it put
// github.com/gorilla/websocket in the tree of a service whose entire design
// premise is that it does not hold a websocket.
//
// Reference:
// https://discord.com/developers/docs/interactions/receiving-and-responding

// interactionType identifies what Discord is asking for.
type interactionType int

const (
	interactionPing               interactionType = 1
	interactionApplicationCommand interactionType = 2
)

// interaction is the sliver of Discord's interaction object this service reads.
//
// Deliberately just the discriminator. The request body is forwarded to Pub/Sub
// verbatim, so whatever consumes the topic decodes the rest; parsing it here
// would be work thrown away. It is also strictly better than what it replaces:
// discordgo's UnmarshalJSON decodes `data` polymorphically per type, so a
// correctly-signed component interaction carrying no `data` failed to parse
// rather than being classified.
type interaction struct {
	Type interactionType `json:"type"`
}

// interactionResponseType is the type field of a response to Discord.
type interactionResponseType int

const (
	responsePong                             interactionResponseType = 1
	responseDeferredChannelMessageWithSource interactionResponseType = 5
)

// interactionResponse is a bare response. Every reply this service sends is a
// type and nothing else -- a Pong, or a deferred acknowledgement -- so there is
// no data field to model.
type interactionResponse struct {
	Type interactionResponseType `json:"type"`
}

// verifyInteraction reports whether r carries a valid Ed25519 signature for key.
//
// Discord signs the concatenation of the X-Signature-Timestamp header and the
// raw request body. The body therefore has to be read here and read again by
// the caller, so it is teed into a buffer and put back on the request.
//
// No cryptography is implemented here: ed25519.Verify is the standard library,
// and it rejects a non-canonical S value itself. The caller is responsible for
// having validated the key length -- ed25519.Verify panics on a wrong-sized key,
// which Config.publicKey checks at construction.
func verifyInteraction(r *http.Request, key ed25519.PublicKey) bool {
	signature := r.Header.Get("X-Signature-Ed25519")
	if signature == "" {
		return false
	}

	sig, err := hex.DecodeString(signature)
	if err != nil || len(sig) != ed25519.SignatureSize {
		return false
	}

	timestamp := r.Header.Get("X-Signature-Timestamp")
	if timestamp == "" {
		return false
	}

	var signed bytes.Buffer
	signed.WriteString(timestamp)

	// net/http closes the request body for a server handler, so this does not.
	var body bytes.Buffer
	if _, err := io.Copy(&signed, io.TeeReader(r.Body, &body)); err != nil {
		return false
	}

	r.Body = io.NopCloser(&body)

	return ed25519.Verify(key, signed.Bytes(), sig)
}
