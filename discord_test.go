package discordbot

import (
	"crypto/ed25519"
	"encoding/hex"
	"io"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
	"time"
)

// Focused tests for verifyInteraction. The handler tests in discordbot_test.go
// cover the happy path incidentally; these cover the paths that only exist
// because this package now owns the verification rather than importing it
// (#56). Replacing a dependency means owning its edge cases.

func testKeypair(t *testing.T) (ed25519.PublicKey, ed25519.PrivateKey) {
	t.Helper()

	pub, priv, err := ed25519.GenerateKey(nil)
	if err != nil {
		t.Fatalf("generating test keypair: %v", err)
	}

	return pub, priv
}

// request builds a request with whatever signature headers the caller wants,
// including deliberately malformed ones.
func request(body, signature, timestamp string) *http.Request {
	req := httptest.NewRequest(http.MethodPost, "/", strings.NewReader(body))

	if signature != "" {
		req.Header.Set("X-Signature-Ed25519", signature)
	}

	if timestamp != "" {
		req.Header.Set("X-Signature-Timestamp", timestamp)
	}

	return req
}

func TestVerifyInteractionAcceptsAValidSignature(t *testing.T) {
	pub, priv := testKeypair(t)

	body := `{"type":1}`
	ts := strconv.FormatInt(time.Now().Unix(), 10)
	sig := hex.EncodeToString(ed25519.Sign(priv, []byte(ts+body)))

	if !verifyInteraction(request(body, sig, ts), pub) {
		t.Error("verifyInteraction = false for a correctly signed request")
	}
}

func TestVerifyInteractionRejects(t *testing.T) {
	pub, priv := testKeypair(t)

	body := `{"type":1}`
	ts := strconv.FormatInt(time.Now().Unix(), 10)
	valid := hex.EncodeToString(ed25519.Sign(priv, []byte(ts+body)))

	// A signature of the right length and encoding that simply is not this
	// message's. Flipping a byte of a valid one keeps every structural check
	// satisfied so the ed25519.Verify call itself is what has to reject it.
	tampered := []byte(valid)
	if tampered[0] == 'a' {
		tampered[0] = 'b'
	} else {
		tampered[0] = 'a'
	}

	otherPub, _ := testKeypair(t)

	for _, tc := range []struct {
		name string
		req  *http.Request
		key  ed25519.PublicKey
	}{
		{"no headers at all", request(body, "", ""), pub},
		{"signature but no timestamp", request(body, valid, ""), pub},
		{"timestamp but no signature", request(body, "", ts), pub},
		{"signature is not hex", request(body, "zzzz", ts), pub},
		{"signature is hex but too short", request(body, "deadbeef", ts), pub},
		{"signature does not verify", request(body, string(tampered), ts), pub},
		{"correct signature, wrong key", request(body, valid, ts), otherPub},
		{"signature is for a different body", request(`{"type":2}`, valid, ts), pub},
		{"signature is for a different timestamp", request(body, valid, "1"), pub},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if verifyInteraction(tc.req, tc.key) {
				t.Error("verifyInteraction = true, want false")
			}
		})
	}
}

// The body has to be readable again afterwards: verification consumes it, and
// ServeHTTP reads it straight after to publish it verbatim. A tee that failed
// to restore it would publish an empty message and pass every other test here.
func TestVerifyInteractionRestoresTheBody(t *testing.T) {
	pub, priv := testKeypair(t)

	body := `{"type":2,"data":{"id":"1","name":"deploy"}}`
	ts := strconv.FormatInt(time.Now().Unix(), 10)
	sig := hex.EncodeToString(ed25519.Sign(priv, []byte(ts+body)))

	req := request(body, sig, ts)

	if !verifyInteraction(req, pub) {
		t.Fatal("verifyInteraction = false for a correctly signed request")
	}

	got, err := io.ReadAll(req.Body)
	if err != nil {
		t.Fatalf("reading the body back: %v", err)
	}

	if string(got) != body {
		t.Errorf("body after verification = %q, want %q", got, body)
	}
}

// A rejected request must also leave the body readable, and there are two
// distinct rejection paths: the structural checks return before the body is
// ever teed, while a signature mismatch returns after. Only the second exercises
// the restoration, so testing one of them proves little about the other.
func TestVerifyInteractionLeavesTheBodyReadableWhenItRejects(t *testing.T) {
	pub, priv := testKeypair(t)

	body := `{"type":1}`
	ts := strconv.FormatInt(time.Now().Unix(), 10)

	// Signed for a different body, so every structural check passes and the
	// rejection happens at ed25519.Verify -- after the tee.
	wrongBodySig := hex.EncodeToString(ed25519.Sign(priv, []byte(ts+`{"type":2}`)))

	for _, tc := range []struct {
		name string
		req  *http.Request
	}{
		{"rejected before the tee", request(body, "deadbeef", ts)},
		{"rejected after the tee", request(body, wrongBodySig, ts)},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if verifyInteraction(tc.req, pub) {
				t.Fatal("verifyInteraction = true, want false")
			}

			got, err := io.ReadAll(tc.req.Body)
			if err != nil {
				t.Fatalf("reading the body back: %v", err)
			}

			if string(got) != body {
				t.Errorf("body after a rejected verification = %q, want %q", got, body)
			}
		})
	}
}
