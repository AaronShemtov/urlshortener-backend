package handler

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/AaronShemtov/urlshortener-backend/internal/cache"
	"github.com/AaronShemtov/urlshortener-backend/internal/turnstile"
)

// fakeSiteverify stands in for Cloudflare. ok decides the verdict.
func fakeSiteverify(t *testing.T, ok bool) string {
	t.Helper()
	body := `{"success":false,"error-codes":["invalid-input-response"]}`
	if ok {
		body = `{"success":true}`
	}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(body))
	}))
	t.Cleanup(srv.Close)
	return srv.URL
}

func gateWithTurnstile(t *testing.T, ok bool, apiKey string) *WriteGate {
	t.Helper()
	return NewWriteGate(turnstile.New("secret").WithEndpoint(fakeSiteverify(t, ok)), apiKey)
}

func post(body string, headers map[string]string) *http.Request {
	r := httptest.NewRequest(http.MethodPost, "/shorten", bytes.NewBufferString(body))
	r.Header.Set("Content-Type", "application/json")
	for k, v := range headers {
		r.Header.Set(k, v)
	}
	return r
}

// -- the regression test for what actually happened -------------------------

func TestARequestWithNoProofAtAllIsRefused(t *testing.T) {
	// This is the shape of every one of the 6002 abusive requests: a bare POST
	// with a URL and nothing else. If this test ever passes a nil error, the
	// August incident is reproducible.
	gate := gateWithTurnstile(t, true, "some-key")
	err := gate.Authorize(context.Background(), post(`{"url":"https://evil.example"}`, nil), "")
	if !errors.Is(err, turnstile.ErrRejected) && !errors.Is(err, ErrUnauthorized) {
		t.Fatalf("a proofless write was not refused: err = %v", err)
	}
	if !refused(err) {
		t.Errorf("refusal should map to 403, but refused() said no: %v", err)
	}
}

func TestAProoflessPostReaches403AndWritesNothing(t *testing.T) {
	store := newMemStoreW()
	wh := NewWriterHandler(store, cache.NewNoopCache(), "http://base", 6,
		gateWithTurnstile(t, true, ""))

	w := httptest.NewRecorder()
	wh.Shorten(w, post(`{"url":"https://evil.example"}`, nil))

	if w.Code != http.StatusForbidden {
		t.Fatalf("status = %d, want 403", w.Code)
	}
	if len(store.m) != 0 {
		t.Errorf("a refused request still wrote %d record(s)", len(store.m))
	}
}

func TestCreateCustomIsGatedToo(t *testing.T) {
	// Easy to secure /shorten and forget its sibling — the attacker would not.
	store := newMemStoreW()
	wh := NewWriterHandler(store, cache.NewNoopCache(), "http://base", 6,
		gateWithTurnstile(t, true, ""))

	w := httptest.NewRecorder()
	wh.CreateCustom(w, post(`{"url":"https://evil.example","code":"free-money"}`, nil))

	if w.Code != http.StatusForbidden {
		t.Fatalf("status = %d, want 403", w.Code)
	}
	if len(store.m) != 0 {
		t.Errorf("a refused request still wrote %d record(s)", len(store.m))
	}
}

// -- the browser path -------------------------------------------------------

func TestAValidTokenInTheBodyPasses(t *testing.T) {
	gate := gateWithTurnstile(t, true, "")
	if err := gate.Authorize(context.Background(), post(`{}`, nil), "good-token"); err != nil {
		t.Fatalf("want nil, got %v", err)
	}
}

func TestAValidTokenInTheHeaderPasses(t *testing.T) {
	// Lets a caller keep the documented JSON body unchanged.
	gate := gateWithTurnstile(t, true, "")
	r := post(`{}`, map[string]string{TokenHeader: "good-token"})
	if err := gate.Authorize(context.Background(), r, ""); err != nil {
		t.Fatalf("want nil, got %v", err)
	}
}

func TestATokenCloudflareDeclinesIsRefused(t *testing.T) {
	gate := gateWithTurnstile(t, false, "")
	err := gate.Authorize(context.Background(), post(`{}`, nil), "forged")
	if !errors.Is(err, turnstile.ErrRejected) {
		t.Fatalf("want ErrRejected, got %v", err)
	}
}

func TestTheWholeShortenFlowWorksWithAToken(t *testing.T) {
	store := newMemStoreW()
	wh := NewWriterHandler(store, cache.NewNoopCache(), "http://base", 6,
		gateWithTurnstile(t, true, ""))

	w := httptest.NewRecorder()
	wh.Shorten(w, post(`{"url":"https://example.com","turnstile_token":"good"}`, nil))

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200, body = %s", w.Code, w.Body.String())
	}
	var got map[string]string
	if err := json.Unmarshal(w.Body.Bytes(), &got); err != nil {
		t.Fatalf("response was not JSON: %v", err)
	}
	if got["short_url"] == "" {
		t.Errorf("no short_url in %v", got)
	}
	if len(store.m) != 1 {
		t.Errorf("stored %d records, want 1", len(store.m))
	}
}

// -- the script path --------------------------------------------------------

func TestTheAPIKeyPassesWithoutATurnstileToken(t *testing.T) {
	// curl cannot solve a challenge, and the homepage documents curl.
	gate := gateWithTurnstile(t, false, "right-key") // siteverify would say no
	r := post(`{}`, map[string]string{APIKeyHeader: "right-key"})
	if err := gate.Authorize(context.Background(), r, ""); err != nil {
		t.Fatalf("want nil, got %v", err)
	}
}

func TestAWrongAPIKeyIsRefusedAndDoesNotFallThroughToTurnstile(t *testing.T) {
	// Falling through would let a key-guessing loop also spend a Cloudflare
	// verification per attempt — our egress, our rate limits, their loop.
	gate := gateWithTurnstile(t, true, "right-key") // siteverify would say yes
	r := post(`{}`, map[string]string{APIKeyHeader: "wrong-key"})
	err := gate.Authorize(context.Background(), r, "any-token")
	if !errors.Is(err, ErrUnauthorized) {
		t.Fatalf("want ErrUnauthorized, got %v", err)
	}
}

func TestAnAPIKeyIsIgnoredWhenNoneIsConfigured(t *testing.T) {
	// Otherwise an empty configured key would match an empty presented one.
	gate := gateWithTurnstile(t, false, "")
	r := post(`{}`, map[string]string{APIKeyHeader: ""})
	if err := gate.Authorize(context.Background(), r, ""); err == nil {
		t.Fatal("an unset API key authorised the request")
	}
}

// -- failure classification -------------------------------------------------

func TestAnUnreachableCloudflareGives503NotAndWritesNothing(t *testing.T) {
	// 403 would tell a legitimate user their browser failed a check that never
	// ran, and would hide an outage among ordinary abuse.
	store := newMemStoreW()
	gate := NewWriteGate(turnstile.New("s").WithEndpoint("http://127.0.0.1:1"), "")
	wh := NewWriterHandler(store, cache.NewNoopCache(), "http://base", 6, gate)

	w := httptest.NewRecorder()
	wh.Shorten(w, post(`{"url":"https://example.com","turnstile_token":"good"}`, nil))

	if w.Code != http.StatusServiceUnavailable {
		t.Fatalf("status = %d, want 503", w.Code)
	}
	if len(store.m) != 0 {
		t.Errorf("write went through while verification was down: %d record(s)", len(store.m))
	}
}

func TestAGateWithNothingConfiguredRefusesEverything(t *testing.T) {
	// Config.Validate should stop this combination from ever being built, but
	// if it slips through the gate must close, not open.
	gate := NewWriteGate(nil, "")
	if err := gate.Authorize(context.Background(), post(`{}`, nil), "token"); err == nil {
		t.Fatal("a gate with no verifier and no key authorised a write")
	}
}

// -- client address ---------------------------------------------------------

func TestClientIPPrefersWhatCloudflareReports(t *testing.T) {
	r := post(`{}`, map[string]string{
		"CF-Connecting-IP": "198.51.100.5",
		"X-Forwarded-For":  "203.0.113.1, 10.0.0.1",
	})
	if got := ClientIP(r); got != "198.51.100.5" {
		t.Errorf("ClientIP = %q, want 198.51.100.5", got)
	}
}

func TestClientIPFallsBackToTheFirstForwardedHop(t *testing.T) {
	r := post(`{}`, map[string]string{"X-Forwarded-For": "203.0.113.1, 10.0.0.1"})
	if got := ClientIP(r); got != "203.0.113.1" {
		t.Errorf("ClientIP = %q, want 203.0.113.1", got)
	}
}

func TestClientIPFallsBackToRemoteAddrWithoutThePort(t *testing.T) {
	r := post(`{}`, nil)
	r.RemoteAddr = "192.0.2.9:54321"
	if got := ClientIP(r); got != "192.0.2.9" {
		t.Errorf("ClientIP = %q, want 192.0.2.9", got)
	}
}

// -- the escape hatch must stay obvious ------------------------------------

func TestTheOpenGateReallyIsOpen(t *testing.T) {
	// Documents the behaviour the existing tests rely on, so that nobody
	// "fixes" it into something that quietly half-checks.
	if err := NewOpenWriteGate().Authorize(context.Background(), post(`{}`, nil), ""); err != nil {
		t.Fatalf("open gate refused a request: %v", err)
	}
}

// -- what the logs are allowed to say --------------------------------------

func TestTheFingerprintDoesNotContainTheAddress(t *testing.T) {
	// These lines go to Loki, which answers unauthenticated queries through a
	// Grafana that is public on purpose. An address in a log line is published.
	r := post(`{}`, map[string]string{"CF-Connecting-IP": "198.51.100.5"})
	fp := clientFingerprint(r)
	if strings.Contains(fp, "198.51.100.5") || strings.Contains(fp, "198") {
		t.Fatalf("fingerprint leaks the address: %q", fp)
	}
	if fp == "" || fp == "unknown" {
		t.Fatalf("fingerprint is useless: %q", fp)
	}
}

func TestTheSameCallerFingerprintsTheSameWay(t *testing.T) {
	// Otherwise "one source or many?" — the only question the log needs to
	// answer — becomes unanswerable.
	a := clientFingerprint(post(`{}`, map[string]string{"CF-Connecting-IP": "198.51.100.5"}))
	b := clientFingerprint(post(`{}`, map[string]string{"CF-Connecting-IP": "198.51.100.5"}))
	c := clientFingerprint(post(`{}`, map[string]string{"CF-Connecting-IP": "203.0.113.9"}))
	if a != b {
		t.Errorf("the same address fingerprinted two ways: %q vs %q", a, b)
	}
	if a == c {
		t.Errorf("two addresses collapsed to one fingerprint: %q", a)
	}
}

// -- what a successful write records ---------------------------------------

func TestASuccessfulWriteReportsWhichProofWasUsed(t *testing.T) {
	// The log line that answers "why was that slow?". It was missing the first
	// time the question came up, and the answer had to be reconstructed from a
	// browser's network tab.
	store := newMemStoreW()
	wh := NewWriterHandler(store, cache.NewNoopCache(), "http://base", 6,
		gateWithTurnstile(t, false, "right-key"))

	w := httptest.NewRecorder()
	wh.Shorten(w, post(`{"url":"https://example.com"}`,
		map[string]string{APIKeyHeader: "right-key"}))
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200: %s", w.Code, w.Body.String())
	}

	proof, _, ok := wh.authorize(httptest.NewRecorder(),
		post(`{}`, map[string]string{APIKeyHeader: "right-key"}), "")
	if !ok || proof != "api-key" {
		t.Errorf("proof = %q ok = %v, want api-key/true", proof, ok)
	}

	proof, _, ok = wh.authorize(httptest.NewRecorder(), post(`{}`, nil), "")
	if ok {
		t.Fatal("a proofless request was authorised")
	}
	if proof != "turnstile" {
		t.Errorf("proof = %q, want turnstile", proof)
	}
}
