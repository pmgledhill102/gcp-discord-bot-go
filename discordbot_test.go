package discordbot

import (
	"crypto/ed25519"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/bwmarrin/discordgo"
)

// These tests exercise handleDiscordMessage directly, and every one of them is
// a regression guard as much as an assertion: the bug they cover was a
// log.Fatalf on the request path, so a reintroduction does not fail an
// assertion -- it calls os.Exit and takes this test binary with it. A suite
// that dies is the signal.
//
// The package reads its configuration in init(), so the environment has to be
// set before the test binary starts. CI does this in test.yml; locally:
//
//	PUBLIC_SIG_KEY=0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef \
//	PUBSUB_PROJECT_ID=test-project-id PUBSUB_TOPIC_NAME=test-topic \
//	PUBSUB_EMULATOR_HOST=127.0.0.1:8085 go test ./...
//
// Wiring configuration through init() is what makes that necessary. #43 covers
// replacing it with a constructor, at which point these tests stop needing an
// environment at all.

// withTestKey swaps in a keypair the test controls and restores the one init()
// built from the environment.
func withTestKey(t *testing.T) ed25519.PrivateKey {
	t.Helper()

	pub, priv, err := ed25519.GenerateKey(nil)
	if err != nil {
		t.Fatalf("generating test keypair: %v", err)
	}

	original := signatureKey
	signatureKey = pub
	t.Cleanup(func() { signatureKey = original })

	return priv
}

// signedRequest builds an interaction request carrying a signature that
// verifies against priv, the way Discord signs one: over timestamp + body.
func signedRequest(t *testing.T, priv ed25519.PrivateKey, body string) *http.Request {
	t.Helper()

	timestamp := strconv.FormatInt(time.Now().Unix(), 10)
	signature := ed25519.Sign(priv, []byte(timestamp+body))

	req := httptest.NewRequest(http.MethodPost, "/handleDiscordMessage", strings.NewReader(body))
	req.Header.Set("X-Signature-Ed25519", hex.EncodeToString(signature))
	req.Header.Set("X-Signature-Timestamp", timestamp)

	return req
}

// The headline case. Discord sends deliberately invalid signatures as a routine
// security check and removes the interactions URL if the response is not 401,
// so this covers a conformance requirement as well as the crash.
func TestInvalidSignatureReturnsUnauthorized(t *testing.T) {
	withTestKey(t)

	req := httptest.NewRequest(http.MethodPost, "/handleDiscordMessage", strings.NewReader(`{"type":1}`))
	req.Header.Set("X-Signature-Ed25519", "deadbeef")
	req.Header.Set("X-Signature-Timestamp", "1")

	rec := httptest.NewRecorder()
	handleDiscordMessage(rec, req)

	if rec.Code != http.StatusUnauthorized {
		t.Errorf("status = %d, want %d", rec.Code, http.StatusUnauthorized)
	}
}

// A request with no signature headers at all takes a different path through
// VerifyInteraction than a malformed one, and must also survive.
func TestMissingSignatureHeadersReturnsUnauthorized(t *testing.T) {
	withTestKey(t)

	req := httptest.NewRequest(http.MethodPost, "/handleDiscordMessage", strings.NewReader(`{"type":1}`))

	rec := httptest.NewRecorder()
	handleDiscordMessage(rec, req)

	if rec.Code != http.StatusUnauthorized {
		t.Errorf("status = %d, want %d", rec.Code, http.StatusUnauthorized)
	}
}

// A correctly signed signature over a body that is not an interaction.
func TestMalformedBodyReturnsBadRequest(t *testing.T) {
	priv := withTestKey(t)

	rec := httptest.NewRecorder()
	handleDiscordMessage(rec, signedRequest(t, priv, "this is not json"))

	if rec.Code != http.StatusBadRequest {
		t.Errorf("status = %d, want %d", rec.Code, http.StatusBadRequest)
	}

	if got := rec.Body.String(); !strings.Contains(got, "malformed interaction payload") {
		t.Errorf("body = %q, want the malformed-payload message", strings.TrimSpace(got))
	}
}

// Buttons, select menus, modal submissions and autocomplete all arrive
// correctly signed and are not application commands. Before this fix, adding a
// single button to the bot turned every click into a container kill.
//
// Each payload carries a populated `data` object on purpose. discordgo's
// Interaction.UnmarshalJSON unmarshals Data for these types, so `{"type":3}`
// alone fails to parse and returns 400 from the malformed-body branch instead
// -- the right status by the wrong route, which would leave this test asserting
// nothing. The response body is checked for the same reason.
func TestUnhandledInteractionTypeReturnsBadRequest(t *testing.T) {
	priv := withTestKey(t)

	for _, tc := range []struct {
		name string
		body string
	}{
		{
			"message component",
			`{"type":3,"data":{"custom_id":"approve","component_type":2}}`,
		},
		{
			"modal submit",
			`{"type":5,"data":{"custom_id":"feedback","components":[]}}`,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			rec := httptest.NewRecorder()
			handleDiscordMessage(rec, signedRequest(t, priv, tc.body))

			if rec.Code != http.StatusBadRequest {
				t.Errorf("status = %d, want %d", rec.Code, http.StatusBadRequest)
			}

			if got := rec.Body.String(); !strings.Contains(got, "unhandled interaction type") {
				t.Errorf("body = %q, want the unhandled-type message -- a parse failure would also give 400", strings.TrimSpace(got))
			}
		})
	}
}

// The PING handshake still has to work: it is how Discord validates the
// endpoint when the URL is first registered. It takes no Pub/Sub round trip,
// so it is asserted end to end.
func TestPingReturnsPong(t *testing.T) {
	priv := withTestKey(t)

	rec := httptest.NewRecorder()
	handleDiscordMessage(rec, signedRequest(t, priv, `{"type":1}`))

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusOK)
	}

	if got := rec.Header().Get("Content-Type"); got != "application/json" {
		t.Errorf("Content-Type = %q, want %q", got, "application/json")
	}

	var res discordgo.InteractionResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &res); err != nil {
		t.Fatalf("unmarshalling response %q: %v", rec.Body.String(), err)
	}

	if res.Type != discordgo.InteractionResponsePong {
		t.Errorf("response type = %d, want %d", res.Type, discordgo.InteractionResponsePong)
	}
}
