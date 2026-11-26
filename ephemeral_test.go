package discordbot

import (
	"context"
	"crypto/ed25519"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// Tests for EPHEMERAL_COMMANDS (#98). They assert the response body byte for
// byte rather than decoding it: the promise to existing deployments is that an
// unlisted command still gets exactly {"type":5}, and a decode would hide a
// stray `"data":{}` or `"data":null`.

const (
	publicAck    = `{"type":5}`
	ephemeralAck = `{"type":5,"data":{"flags":64}}`
)

// newEphemeralHandler builds a test Handler with EphemeralCommands set to value.
func newEphemeralHandler(t *testing.T, value string) (*Handler, *fakePublisher, ed25519.PrivateKey) {
	t.Helper()

	h, fake, priv := newTestHandler(t)

	set, err := parseEphemeralCommands(value)
	if err != nil {
		t.Fatalf("parseEphemeralCommands(%q) = %v", value, err)
	}

	h.ephemeral = set

	return h, fake, priv
}

func TestEphemeralCommandAcknowledgement(t *testing.T) {
	for _, tc := range []struct {
		name    string
		config  string
		body    string
		wantAck string
	}{
		// Unset: byte-for-byte the pre-#98 behaviour, whatever the command.
		{"unset, top-level command", "", `{"type":2,"data":{"id":"1","name":"deploy"}}`, publicAck},
		{"unset, would-be listed subcommand", "", `{"type":2,"data":{"name":"dfc","options":[{"name":"stats","type":1}]}}`, publicAck},
		{"whitespace-only is unset", "  ", `{"type":2,"data":{"name":"dfc","options":[{"name":"stats","type":1}]}}`, publicAck},

		// "dfc stats": only that exact subcommand.
		{"listed subcommand", "dfc stats", `{"type":2,"data":{"name":"dfc","options":[{"name":"stats","type":1}]}}`, ephemeralAck},
		{"sibling subcommand", "dfc stats", `{"type":2,"data":{"name":"dfc","options":[{"name":"status","type":1}]}}`, publicAck},
		{"command with no options", "dfc stats", `{"type":2,"data":{"name":"dfc"}}`, publicAck},
		{"command with empty options", "dfc stats", `{"type":2,"data":{"name":"dfc","options":[]}}`, publicAck},
		{"same subcommand, other command", "dfc stats", `{"type":2,"data":{"name":"other","options":[{"name":"stats","type":1}]}}`, publicAck},
		{"subcommand in a later option only", "dfc stats", `{"type":2,"data":{"name":"dfc","options":[{"name":"x","type":3},{"name":"stats","type":1}]}}`, publicAck},

		// A first option that is not a subcommand never matches the two-word form.
		{"string option named like the subcommand", "dfc stats", `{"type":2,"data":{"name":"dfc","options":[{"name":"stats","type":3,"value":"x"}]}}`, publicAck},
		{"subcommand group", "dfc stats", `{"type":2,"data":{"name":"dfc","options":[{"name":"stats","type":2,"options":[{"name":"all","type":1}]}]}}`, publicAck},

		// A top-level entry matches the command with any subcommand or none.
		{"top-level entry, no options", "deploy", `{"type":2,"data":{"id":"1","name":"deploy"}}`, ephemeralAck},
		{"top-level entry, with subcommand", "deploy", `{"type":2,"data":{"name":"deploy","options":[{"name":"prod","type":1}]}}`, ephemeralAck},
		{"top-level entry, with plain option", "deploy", `{"type":2,"data":{"name":"deploy","options":[{"name":"env","type":3,"value":"prod"}]}}`, ephemeralAck},
		{"top-level entry, other command", "deploy", `{"type":2,"data":{"name":"deployer"}}`, publicAck},

		// The DFC rollout value.
		{"list, first entry", "dfc stats,dfc-dev stats", `{"type":2,"data":{"name":"dfc","options":[{"name":"stats","type":1}]}}`, ephemeralAck},
		{"list, second entry", "dfc stats,dfc-dev stats", `{"type":2,"data":{"name":"dfc-dev","options":[{"name":"stats","type":1}]}}`, ephemeralAck},
		{"list, unlisted", "dfc stats,dfc-dev stats", `{"type":2,"data":{"name":"dfc-dev","options":[{"name":"status","type":1}]}}`, publicAck},
		{"list with spaces round entries", " dfc stats , deploy ", `{"type":2,"data":{"name":"deploy"}}`, ephemeralAck},

		// Missing or odd data never errors; it is simply not listed.
		{"no data", "dfc stats", `{"type":2}`, publicAck},
		{"null data", "dfc stats", `{"type":2,"data":null}`, publicAck},
		{"data without a name", "dfc stats", `{"type":2,"data":{}}`, publicAck},
		{"data of the wrong shape", "dfc stats", `{"type":2,"data":"dfc stats"}`, publicAck},
		{"options of the wrong shape", "dfc stats", `{"type":2,"data":{"name":"dfc","options":{"name":"stats"}}}`, publicAck},
	} {
		t.Run(tc.name, func(t *testing.T) {
			h, fake, priv := newEphemeralHandler(t, tc.config)

			rec := httptest.NewRecorder()
			h.ServeHTTP(rec, signedRequest(t, priv, tc.body))

			if rec.Code != http.StatusOK {
				t.Fatalf("status = %d, want %d (body %q)", rec.Code, http.StatusOK, rec.Body.String())
			}

			if got := rec.Body.String(); got != tc.wantAck {
				t.Errorf("body = %s, want %s", got, tc.wantAck)
			}

			if got := rec.Header().Get("Content-Type"); got != "application/json" {
				t.Errorf("Content-Type = %q, want %q", got, "application/json")
			}

			// Whatever the visibility, the body still reaches the topic verbatim.
			if len(fake.published) != 1 || string(fake.published[0]) != tc.body {
				t.Errorf("published %q, want exactly the request body %q", fake.published, tc.body)
			}
		})
	}
}

// Configuring the feature must not touch any path other than the final
// acknowledgement: not the 401, the 400s, the 500 or the Pong.
func TestEphemeralCommandsLeaveOtherPathsAlone(t *testing.T) {
	const config = "dfc stats,deploy"

	listed := `{"type":2,"data":{"name":"dfc","options":[{"name":"stats","type":1}]}}`

	t.Run("invalid signature", func(t *testing.T) {
		h, _, _ := newEphemeralHandler(t, config)

		req := httptest.NewRequest(http.MethodPost, "/handleDiscordMessage", strings.NewReader(listed))
		req.Header.Set("X-Signature-Ed25519", "deadbeef")
		req.Header.Set("X-Signature-Timestamp", "1")

		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, req)

		if rec.Code != http.StatusUnauthorized {
			t.Errorf("status = %d, want %d", rec.Code, http.StatusUnauthorized)
		}
	})

	t.Run("malformed body", func(t *testing.T) {
		h, _, priv := newEphemeralHandler(t, config)

		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, signedRequest(t, priv, "not json"))

		if rec.Code != http.StatusBadRequest || !strings.Contains(rec.Body.String(), "malformed interaction payload") {
			t.Errorf("got %d %q, want 400 with the malformed-payload message", rec.Code, rec.Body.String())
		}
	})

	t.Run("unhandled type carrying a listed command's data", func(t *testing.T) {
		h, fake, priv := newEphemeralHandler(t, config)

		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, signedRequest(t, priv, `{"type":4,"data":{"name":"dfc","options":[{"name":"stats","type":1}]}}`))

		if rec.Code != http.StatusBadRequest || !strings.Contains(rec.Body.String(), "unhandled interaction type") {
			t.Errorf("got %d %q, want 400 with the unhandled-type message", rec.Code, rec.Body.String())
		}

		if len(fake.published) != 0 {
			t.Errorf("published %d messages, want 0", len(fake.published))
		}
	})

	t.Run("publish failure on a listed command", func(t *testing.T) {
		h, fake, priv := newEphemeralHandler(t, config)
		fake.err = errors.New("topic unavailable")

		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, signedRequest(t, priv, listed))

		if rec.Code != http.StatusInternalServerError || !strings.Contains(rec.Body.String(), "failed to queue interaction") {
			t.Errorf("got %d %q, want 500 with the queue-failure message", rec.Code, rec.Body.String())
		}
	})

	t.Run("ping", func(t *testing.T) {
		h, _, priv := newEphemeralHandler(t, config)

		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, signedRequest(t, priv, `{"type":1}`))

		if got := rec.Body.String(); rec.Code != http.StatusOK || got != `{"type":1}` {
			t.Errorf("got %d %s, want 200 {\"type\":1}", rec.Code, got)
		}
	})
}

func TestParseEphemeralCommands(t *testing.T) {
	for _, tc := range []struct {
		name    string
		value   string
		want    []string
		wantErr string
	}{
		{"empty", "", nil, ""},
		{"whitespace only", " \t ", nil, ""},
		{"one top-level", "deploy", []string{"deploy"}, ""},
		{"one subcommand", "dfc stats", []string{"dfc stats"}, ""},
		{"two entries", "dfc stats,dfc-dev stats", []string{"dfc stats", "dfc-dev stats"}, ""},
		{"spacing normalised", "  dfc   stats ,deploy", []string{"dfc stats", "deploy"}, ""},
		{"duplicate entries", "deploy,deploy", []string{"deploy"}, ""},

		{"three words", "dfc  stats  x", nil, "3 words"},
		{"lone comma", ",", nil, "entry 1 is empty"},
		{"trailing comma", "dfc stats,", nil, "entry 2 is empty"},
		{"blank middle entry", "dfc stats, ,deploy", nil, "entry 2 is empty"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, err := parseEphemeralCommands(tc.value)

			if tc.wantErr != "" {
				if err == nil {
					t.Fatalf("parseEphemeralCommands(%q) = %v, want an error", tc.value, got)
				}

				if !strings.Contains(err.Error(), tc.wantErr) || !strings.Contains(err.Error(), EnvEphemeralCommands) {
					t.Errorf("error %q, want it to name %s and contain %q", err, EnvEphemeralCommands, tc.wantErr)
				}

				return
			}

			if err != nil {
				t.Fatalf("parseEphemeralCommands(%q) = %v, want no error", tc.value, err)
			}

			if len(got) != len(tc.want) {
				t.Errorf("got %d entries %v, want %v", len(got), got, tc.want)
			}

			for _, key := range tc.want {
				if _, ok := got[key]; !ok {
					t.Errorf("missing entry %q in %v", key, got)
				}
			}
		})
	}
}

// A malformed value fails New rather than surfacing later on the request path.
// Validation runs before the Pub/Sub client is created, so this needs no
// emulator or credentials.
func TestNewRejectsMalformedEphemeralCommands(t *testing.T) {
	for _, value := range []string{"dfc  stats  x", ",", "dfc stats,"} {
		t.Run(value, func(t *testing.T) {
			h, err := New(context.Background(), Config{
				PublicKey:         strings.Repeat("ab", ed25519.PublicKeySize),
				ProjectID:         "a-project",
				TopicName:         "a-topic",
				EphemeralCommands: value,
			})
			if err == nil {
				_ = h.Close()
				t.Fatalf("New() with EphemeralCommands %q = nil error, want an error", value)
			}

			if !strings.Contains(err.Error(), EnvEphemeralCommands) {
				t.Errorf("error %q does not name %s", err, EnvEphemeralCommands)
			}
		})
	}
}

// The variable is optional: unset it is not reported missing, and set it is
// read through to Config.
func TestConfigFromEnvEphemeralCommands(t *testing.T) {
	t.Setenv(EnvPublicKey, strings.Repeat("ab", ed25519.PublicKeySize))
	t.Setenv(EnvProjectID, "a-project")
	t.Setenv(EnvTopicName, "a-topic")

	t.Run("unset is not missing", func(t *testing.T) {
		t.Setenv(EnvEphemeralCommands, "")

		cfg, err := ConfigFromEnv()
		if err != nil {
			t.Fatalf("ConfigFromEnv() = %v, want no error", err)
		}

		if cfg.EphemeralCommands != "" {
			t.Errorf("EphemeralCommands = %q, want empty", cfg.EphemeralCommands)
		}
	})

	t.Run("set is read", func(t *testing.T) {
		t.Setenv(EnvEphemeralCommands, "dfc stats,dfc-dev stats")

		cfg, err := ConfigFromEnv()
		if err != nil {
			t.Fatalf("ConfigFromEnv() = %v, want no error", err)
		}

		if cfg.EphemeralCommands != "dfc stats,dfc-dev stats" {
			t.Errorf("EphemeralCommands = %q", cfg.EphemeralCommands)
		}
	})

	t.Run("missing required still names only the required", func(t *testing.T) {
		t.Setenv(EnvTopicName, "")
		t.Setenv(EnvEphemeralCommands, "")

		_, err := ConfigFromEnv()
		if err == nil {
			t.Fatal("ConfigFromEnv() = nil error, want an error")
		}

		if strings.Contains(err.Error(), EnvEphemeralCommands) {
			t.Errorf("error %q names the optional %s as missing", err, EnvEphemeralCommands)
		}
	})
}
