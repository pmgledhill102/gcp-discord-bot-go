// Command client drives the cold-start comparison in bench/.
//
//	client keygen -key FILE              write a test Ed25519 seed, print the public key
//	client smoke  -key FILE URL...       check each service honours the handler contract
//	client run    -key FILE -out F URL...  one cold request then warm ones, per service
//
// Every request carries an X-Cloud-Trace-Context header, recorded alongside the
// client-side timings, so that each one can be matched to its Cloud Run request
// log entry. The log supplies the server-side latency and the instance ID: the
// latency excludes this client's network path, and the instance ID is what
// proves a "cold" request really did start a new instance.
package main

import (
	"bytes"
	"crypto/ed25519"
	"crypto/rand"
	"crypto/tls"
	"encoding/hex"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"net/http"
	"net/http/httptrace"
	"os"
	"strconv"
	"strings"
	"sync"
	"time"
)

func main() {
	if len(os.Args) < 2 {
		usage()
	}

	var err error
	switch os.Args[1] {
	case "keygen":
		err = keygen(os.Args[2:])
	case "smoke":
		err = smoke(os.Args[2:])
	case "run":
		err = run(os.Args[2:])
	default:
		usage()
	}

	if err != nil {
		fmt.Fprintln(os.Stderr, "error:", err)
		os.Exit(1)
	}
}

func usage() {
	fmt.Fprintln(os.Stderr, "usage: client keygen|smoke|run [flags] [URL...]")
	os.Exit(2)
}

func keygen(args []string) error {
	fs := flag.NewFlagSet("keygen", flag.ExitOnError)
	keyFile := fs.String("key", "", "file to write the private seed to")
	_ = fs.Parse(args)

	if *keyFile == "" {
		return errors.New("-key is required")
	}

	pub, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		return err
	}

	if err := os.WriteFile(*keyFile, []byte(hex.EncodeToString(priv.Seed())), 0o600); err != nil {
		return err
	}

	fmt.Println(hex.EncodeToString(pub))

	return nil
}

func loadKey(file string) (ed25519.PrivateKey, error) {
	b, err := os.ReadFile(file) // #nosec G304 -- the operator names their own key file
	if err != nil {
		return nil, err
	}

	seed, err := hex.DecodeString(strings.TrimSpace(string(b)))
	if err != nil || len(seed) != ed25519.SeedSize {
		return nil, fmt.Errorf("%s does not hold a hex Ed25519 seed", file)
	}

	return ed25519.NewKeyFromSeed(seed), nil
}

// command returns a body shaped like a real Discord slash command, about the
// size of one, with a unique interaction ID.
func command() []byte {
	id := strconv.FormatInt(time.Now().UnixNano(), 10)

	body, _ := json.Marshal(map[string]any{
		"app_permissions": "562949953601536",
		"application_id":  "1100000000000000000",
		"channel": map[string]any{
			"id": "1100000000000000001", "name": "general", "type": 0,
			"guild_id": "1100000000000000002", "nsfw": false, "position": 0,
		},
		"channel_id": "1100000000000000001",
		"data": map[string]any{
			"id": "1100000000000000003", "name": "bench", "type": 1,
			"options": []map[string]any{{"name": "run", "type": 1, "options": []any{}}},
		},
		"entitlements": []any{},
		"guild_id":     "1100000000000000002",
		"guild_locale": "en-GB",
		"id":           id,
		"locale":       "en-GB",
		"member": map[string]any{
			"avatar": nil, "deaf": false, "flags": 0, "joined_at": "2024-01-01T00:00:00.000000+00:00",
			"mute": false, "nick": nil, "pending": false, "permissions": "2251799813685247",
			"roles": []string{"1100000000000000004"},
			"user": map[string]any{
				"avatar": "0123456789abcdef0123456789abcdef", "discriminator": "0",
				"global_name": "Bench", "id": "1100000000000000005", "public_flags": 0,
				"username": "bench",
			},
		},
		"token":   strings.Repeat("x", 180),
		"type":    2,
		"version": 1,
	})

	return body
}

type result struct {
	Service   string    `json:"service"`
	Label     string    `json:"label"`
	Kind      string    `json:"kind"` // "cold" or "warm"
	Seq       int       `json:"seq"`
	Start     time.Time `json:"start"`
	Trace     string    `json:"trace"`
	Status    int       `json:"status"`
	Body      string    `json:"body"`
	TTFBms    float64   `json:"ttfb_ms"`
	TotalMs   float64   `json:"total_ms"`
	ConnectMs float64   `json:"connect_ms"`
	Err       string    `json:"err,omitempty"`
}

type sender struct {
	key    ed25519.PrivateKey
	client *http.Client
}

func newSender(key ed25519.PrivateKey) *sender {
	return &sender{
		key: key,
		// A fresh connection per request, so cold and warm requests pay the
		// same connection set-up and differ only in what the server does.
		client: &http.Client{
			Timeout:   60 * time.Second,
			Transport: &http.Transport{DisableKeepAlives: true, Proxy: http.ProxyFromEnvironment},
		},
	}
}

// send posts body to url, signed unless badSig is set.
func (s *sender) send(url string, body []byte, badSig bool) result {
	ts := strconv.FormatInt(time.Now().Unix(), 10)
	sig := ed25519.Sign(s.key, append([]byte(ts), body...))

	if badSig {
		sig[0] ^= 0xff
	}

	trace := make([]byte, 16)
	_, _ = rand.Read(trace)

	res := result{Trace: hex.EncodeToString(trace)}

	req, err := http.NewRequest(http.MethodPost, url, bytes.NewReader(body))
	if err != nil {
		res.Err = err.Error()
		return res
	}

	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("User-Agent", "Discord-Interactions/1.0 (+https://discord.com)")
	req.Header.Set("X-Signature-Ed25519", hex.EncodeToString(sig))
	req.Header.Set("X-Signature-Timestamp", ts)
	req.Header.Set("X-Cloud-Trace-Context", res.Trace+"/1;o=1")

	var connStart, connDone, firstByte time.Time

	req = req.WithContext(httptrace.WithClientTrace(req.Context(), &httptrace.ClientTrace{
		ConnectStart:         func(string, string) { connStart = time.Now() },
		TLSHandshakeDone:     func(_ tls.ConnectionState, _ error) { connDone = time.Now() },
		GotFirstResponseByte: func() { firstByte = time.Now() },
	}))

	res.Start = time.Now()

	resp, err := s.client.Do(req)
	if err != nil {
		res.Err = err.Error()
		res.TotalMs = ms(time.Since(res.Start))

		return res
	}
	defer func() { _ = resp.Body.Close() }()

	b, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
	end := time.Now()

	res.Status = resp.StatusCode
	res.Body = strings.TrimSpace(string(b))
	res.TotalMs = ms(end.Sub(res.Start))
	res.TTFBms = ms(firstByte.Sub(res.Start))

	if !connStart.IsZero() && !connDone.IsZero() {
		res.ConnectMs = ms(connDone.Sub(connStart))
	}

	return res
}

func ms(d time.Duration) float64 { return float64(d.Microseconds()) / 1000 }

func smoke(args []string) error {
	fs := flag.NewFlagSet("smoke", flag.ExitOnError)
	keyFile := fs.String("key", "", "private seed written by keygen")
	_ = fs.Parse(args)

	key, err := loadKey(*keyFile)
	if err != nil {
		return err
	}

	s := newSender(key)
	checks := []struct {
		name         string
		body         []byte
		badSig       bool
		status       int
		bodyContains string
	}{
		{"bad signature", []byte(`{"type":1}`), true, http.StatusUnauthorized, "invalid request signature"},
		{"ping", []byte(`{"type":1}`), false, http.StatusOK, `{"type":1}`},
		{"malformed", []byte(`{"type":`), false, http.StatusBadRequest, "malformed interaction payload"},
		{"unhandled", []byte(`{"type":3}`), false, http.StatusBadRequest, "unhandled interaction type"},
		{"command", command(), false, http.StatusOK, `{"type":5}`},
	}

	failed := 0

	for _, url := range fs.Args() {
		for _, c := range checks {
			r := s.send(url, c.body, c.badSig)
			ok := r.Err == "" && r.Status == c.status && strings.Contains(r.Body, c.bodyContains)

			mark := "ok  "
			if !ok {
				mark = "FAIL"
				failed++
			}

			fmt.Printf("%s %-60s %-14s %d %q %s (%.0f ms)\n", mark, url, c.name, r.Status, r.Body, r.Err, r.TotalMs)
		}
	}

	if failed > 0 {
		return fmt.Errorf("%d checks failed", failed)
	}

	return nil
}

func run(args []string) error {
	fs := flag.NewFlagSet("run", flag.ExitOnError)
	keyFile := fs.String("key", "", "private seed written by keygen")
	out := fs.String("out", "", "JSON Lines file to append results to")
	label := fs.String("label", "", "label recorded with every result, e.g. round-1")
	warm := fs.Int("warm", 5, "warm requests to send after the cold one")
	gap := fs.Duration("gap", 500*time.Millisecond, "pause between warm requests")
	_ = fs.Parse(args)

	key, err := loadKey(*keyFile)
	if err != nil {
		return err
	}

	if *out == "" {
		return errors.New("-out is required")
	}

	f, err := os.OpenFile(*out, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o600)
	if err != nil {
		return err
	}
	defer func() { _ = f.Close() }()

	s := newSender(key)

	var (
		mu sync.Mutex
		wg sync.WaitGroup
	)

	enc := json.NewEncoder(f)
	record := func(r result) {
		mu.Lock()
		defer mu.Unlock()

		_ = enc.Encode(r)
		fmt.Printf("%-18s %-4s %d %3d %-12q ttfb %7.0f ms  total %7.0f ms %s\n",
			r.Service, r.Kind, r.Seq, r.Status, r.Body, r.TTFBms, r.TotalMs, r.Err)
	}

	// Every service at once: they are independent Cloud Run services, and
	// firing together keeps the idle period identical for all of them.
	for _, url := range fs.Args() {
		wg.Go(func() {
			name := serviceName(url)

			for i := 0; i <= *warm; i++ {
				kind := "warm"
				if i == 0 {
					kind = "cold"
				} else {
					time.Sleep(*gap)
				}

				r := s.send(url, command(), false)
				r.Service, r.Label, r.Kind, r.Seq = name, *label, kind, i
				record(r)
			}
		})
	}

	wg.Wait()

	return nil
}

// serviceName turns https://bench-rust-123.europe-west2.run.app/ into "rust".
func serviceName(url string) string {
	host := strings.TrimPrefix(strings.TrimPrefix(url, "https://"), "http://")
	host, _, _ = strings.Cut(host, ".")
	host = strings.TrimPrefix(host, "bench-")

	if i := strings.LastIndex(host, "-"); i > 0 {
		if _, err := strconv.Atoi(host[i+1:]); err == nil {
			host = host[:i]
		}
	}

	return host
}
