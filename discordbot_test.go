package discordbot

import (
	"context"
	"crypto/ed25519"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"strconv"
	"strings"
	"testing"
	"time"

	"cloud.google.com/go/pubsub"
)

// The request-path tests are regression guards as much as assertions. The bug
// they cover (#49) was a log.Fatalf on the request path, so a reintroduction
// does not fail an assertion -- it calls os.Exit and takes the test binary with
// it. A suite that dies is the signal.
//
// They need no environment: the handler is constructed directly, which is the
// point of #43. Only TestPublishesToTopic touches the emulator, and it skips
// itself when there is not one.

// fakePublisher stands in for Pub/Sub so the request path can be tested without
// a broker.
type fakePublisher struct {
	published [][]byte
	err       error
	closed    bool
}

func (f *fakePublisher) Publish(_ context.Context, data []byte) (string, error) {
	if f.err != nil {
		return "", f.err
	}

	f.published = append(f.published, data)

	return "fake-message-id", nil
}

func (f *fakePublisher) Close() error {
	f.closed = true

	return nil
}

// newTestHandler builds a Handler wired to a fake publisher, with a keypair the
// test controls.
func newTestHandler(t *testing.T) (*Handler, *fakePublisher, ed25519.PrivateKey) {
	t.Helper()

	pub, priv, err := ed25519.GenerateKey(nil)
	if err != nil {
		t.Fatalf("generating test keypair: %v", err)
	}

	fake := &fakePublisher{}

	return &Handler{key: pub, pub: fake}, fake, priv
}

// signedRequest builds an interaction request signed the way Discord signs one:
// over timestamp + body.
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
// so this is a conformance requirement as well as a crash guard.
func TestInvalidSignatureReturnsUnauthorized(t *testing.T) {
	h, _, _ := newTestHandler(t)

	req := httptest.NewRequest(http.MethodPost, "/handleDiscordMessage", strings.NewReader(`{"type":1}`))
	req.Header.Set("X-Signature-Ed25519", "deadbeef")
	req.Header.Set("X-Signature-Timestamp", "1")

	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusUnauthorized {
		t.Errorf("status = %d, want %d", rec.Code, http.StatusUnauthorized)
	}
}

// No signature headers at all takes a different path through VerifyInteraction
// than a malformed one, and must also survive.
func TestMissingSignatureHeadersReturnsUnauthorized(t *testing.T) {
	h, _, _ := newTestHandler(t)

	req := httptest.NewRequest(http.MethodPost, "/handleDiscordMessage", strings.NewReader(`{"type":1}`))

	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusUnauthorized {
		t.Errorf("status = %d, want %d", rec.Code, http.StatusUnauthorized)
	}
}

// A signature that verifies, over a body that is not an interaction.
func TestMalformedBodyReturnsBadRequest(t *testing.T) {
	h, _, priv := newTestHandler(t)

	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, signedRequest(t, priv, "this is not json"))

	if rec.Code != http.StatusBadRequest {
		t.Errorf("status = %d, want %d", rec.Code, http.StatusBadRequest)
	}

	if got := rec.Body.String(); !strings.Contains(got, "malformed interaction payload") {
		t.Errorf("body = %q, want the malformed-payload message", strings.TrimSpace(got))
	}
}

// Buttons, select menus and modal submissions arrive correctly signed and are
// not application commands. Before #49, adding one button turned every click
// into a container kill.
//
// These payloads carried a populated `data` object until #56, purely because
// discordgo's decoder unmarshalled Data per type and so rejected `{"type":3}`
// outright -- the test then passed from the malformed-body branch and guarded
// nothing. This package decodes only the discriminator, so the bare form now
// reaches the branch under test. The body assertion stays: two branches still
// return 400, and a test that cannot tell them apart is the bug that was here
// before.
func TestUnhandledInteractionTypeReturnsBadRequest(t *testing.T) {
	h, fake, priv := newTestHandler(t)

	for _, tc := range []struct {
		name string
		body string
	}{
		{"message component", `{"type":3}`},
		{"modal submit", `{"type":5}`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			rec := httptest.NewRecorder()
			h.ServeHTTP(rec, signedRequest(t, priv, tc.body))

			if rec.Code != http.StatusBadRequest {
				t.Errorf("status = %d, want %d", rec.Code, http.StatusBadRequest)
			}

			if got := rec.Body.String(); !strings.Contains(got, "unhandled interaction type") {
				t.Errorf("body = %q, want the unhandled-type message -- a parse failure also gives 400", strings.TrimSpace(got))
			}
		})
	}

	if len(fake.published) != 0 {
		t.Errorf("published %d messages, want 0 -- nothing unhandled should reach the topic", len(fake.published))
	}
}

// The PING handshake is how Discord validates the endpoint when the URL is
// first registered, so it has to keep working.
func TestPingReturnsPong(t *testing.T) {
	h, fake, priv := newTestHandler(t)

	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, signedRequest(t, priv, `{"type":1}`))

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusOK)
	}

	if got := rec.Header().Get("Content-Type"); got != "application/json" {
		t.Errorf("Content-Type = %q, want %q", got, "application/json")
	}

	var res interactionResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &res); err != nil {
		t.Fatalf("unmarshalling response %q: %v", rec.Body.String(), err)
	}

	if res.Type != responsePong {
		t.Errorf("response type = %d, want %d", res.Type, responsePong)
	}

	if len(fake.published) != 0 {
		t.Errorf("published %d messages, want 0 -- a PING should not reach the topic", len(fake.published))
	}
}

// The happy path: an application command is published verbatim and acknowledged
// with a deferred response, which is what buys the follow-up past Discord's 3
// second deadline.
func TestApplicationCommandPublishesAndDefers(t *testing.T) {
	h, fake, priv := newTestHandler(t)

	body := `{"type":2,"data":{"id":"1","name":"deploy"}}`

	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, signedRequest(t, priv, body))

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusOK)
	}

	if len(fake.published) != 1 {
		t.Fatalf("published %d messages, want 1", len(fake.published))
	}

	if got := string(fake.published[0]); got != body {
		t.Errorf("published %q, want the request body verbatim %q", got, body)
	}

	var res interactionResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &res); err != nil {
		t.Fatalf("unmarshalling response %q: %v", rec.Body.String(), err)
	}

	if res.Type != responseDeferredChannelMessageWithSource {
		t.Errorf("response type = %d, want %d",
			res.Type, responseDeferredChannelMessageWithSource)
	}
}

// A Pub/Sub outage is a failed request, not a dead process.
func TestPublishFailureReturnsInternalServerError(t *testing.T) {
	h, fake, priv := newTestHandler(t)
	fake.err = errors.New("topic unavailable")

	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, signedRequest(t, priv, `{"type":2,"data":{"id":"1","name":"deploy"}}`))

	if rec.Code != http.StatusInternalServerError {
		t.Errorf("status = %d, want %d", rec.Code, http.StatusInternalServerError)
	}
}

func TestConfigPublicKeyValidation(t *testing.T) {
	valid := strings.Repeat("ab", ed25519.PublicKeySize)

	for _, tc := range []struct {
		name    string
		key     string
		wantErr bool
	}{
		{"valid", valid, false},
		{"not hex", "nothexatall", true},
		{"too short", "abcd", true},
		{"too long", valid + "ab", true},
		{"empty", "", true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, err := Config{PublicKey: tc.key}.publicKey()

			if tc.wantErr && err == nil {
				t.Errorf("publicKey() = nil error, want an error")
			}

			if !tc.wantErr && err != nil {
				t.Errorf("publicKey() = %v, want no error", err)
			}
		})
	}
}

// A missing variable should name itself. Reporting only the first would send
// someone round the loop three times.
func TestConfigFromEnvNamesEveryMissingVariable(t *testing.T) {
	t.Setenv(EnvPublicKey, "")
	t.Setenv(EnvProjectID, "")
	t.Setenv(EnvTopicName, "")

	_, err := ConfigFromEnv()
	if err == nil {
		t.Fatal("ConfigFromEnv() = nil error, want an error")
	}

	for _, name := range []string{EnvPublicKey, EnvProjectID, EnvTopicName} {
		if !strings.Contains(err.Error(), name) {
			t.Errorf("error %q does not mention %s", err, name)
		}
	}
}

func TestConfigFromEnvReadsAllThree(t *testing.T) {
	key := strings.Repeat("ab", ed25519.PublicKeySize)

	t.Setenv(EnvPublicKey, key)
	t.Setenv(EnvProjectID, "a-project")
	t.Setenv(EnvTopicName, "a-topic")

	cfg, err := ConfigFromEnv()
	if err != nil {
		t.Fatalf("ConfigFromEnv() = %v, want no error", err)
	}

	if cfg != (Config{PublicKey: key, ProjectID: "a-project", TopicName: "a-topic"}) {
		t.Errorf("ConfigFromEnv() = %+v", cfg)
	}
}

// Covers topicPublisher, which every test above bypasses via the fake. Runs
// against the Pub/Sub emulator that test.yml starts, and skips when there is
// not one so `go test ./...` still works on a laptop with nothing running.
func TestPublishesToTopic(t *testing.T) {
	if os.Getenv("PUBSUB_EMULATOR_HOST") == "" {
		// Skipping is a local convenience, and a liability anywhere it is
		// supposed to run: a test that silently opts out is a test that can
		// stop covering anything without anyone noticing. test.yml starts an
		// emulator, so its absence under CI is a broken workflow rather than a
		// missing local tool, and should fail rather than pass quietly.
		if os.Getenv("CI") != "" {
			t.Fatal("PUBSUB_EMULATOR_HOST is not set but CI is: the emulator should be running, see .github/workflows/test.yml")
		}

		t.Skip("PUBSUB_EMULATOR_HOST is not set; skipping the Pub/Sub integration test")
	}

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	projectID := os.Getenv(EnvProjectID)
	if projectID == "" {
		projectID = "test-project-id"
	}

	topicName := fmt.Sprintf("integration-%d", time.Now().UnixNano())

	client, err := pubsub.NewClient(ctx, projectID)
	if err != nil {
		t.Fatalf("connecting to the emulator: %v", err)
	}
	defer func() { _ = client.Close() }()

	// The name carries a nanosecond timestamp, so this cannot collide with a
	// previous run and an AlreadyExists error would be a real failure.
	topic, err := client.CreateTopic(ctx, topicName)
	if err != nil {
		t.Fatalf("creating topic: %v", err)
	}
	defer func() { _ = topic.Delete(ctx) }()

	sub, err := client.CreateSubscription(ctx, topicName+"-sub", pubsub.SubscriptionConfig{Topic: topic})
	if err != nil {
		t.Fatalf("creating subscription: %v", err)
	}
	defer func() { _ = sub.Delete(ctx) }()

	pub, priv, err := ed25519.GenerateKey(nil)
	if err != nil {
		t.Fatalf("generating test keypair: %v", err)
	}

	// Exercises New() rather than the struct literal, so the real Pub/Sub
	// wiring is under test and not just the handler logic.
	h, err := New(ctx, Config{
		PublicKey: hex.EncodeToString(pub),
		ProjectID: projectID,
		TopicName: topicName,
	})
	if err != nil {
		t.Fatalf("New() = %v", err)
	}
	defer func() { _ = h.Close() }()

	body := `{"type":2,"data":{"id":"1","name":"deploy"}}`

	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, signedRequest(t, priv, body))

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d (body %q)", rec.Code, http.StatusOK, rec.Body.String())
	}

	receiveCtx, stop := context.WithTimeout(ctx, 20*time.Second)
	defer stop()

	var got string

	err = sub.Receive(receiveCtx, func(_ context.Context, m *pubsub.Message) {
		got = string(m.Data)
		m.Ack()
		stop()
	})

	// stop() cancels receiveCtx to end Receive once the message has arrived, so
	// a cancellation here is the success path rather than a failure.
	if err != nil && !errors.Is(err, context.Canceled) {
		t.Fatalf("receiving: %v", err)
	}

	if got == "" {
		t.Fatal("no message received before the timeout")
	}

	if got != body {
		t.Errorf("received %q, want %q", got, body)
	}
}
