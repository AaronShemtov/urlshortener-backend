package turnstile

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// fakeCloudflare stands in for siteverify and records what it was sent.
func fakeCloudflare(t *testing.T, status int, body string, seen *http.Request) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if err := r.ParseForm(); err != nil {
			t.Errorf("siteverify got an unparseable body: %v", err)
		}
		if seen != nil {
			*seen = *r
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(status)
		_, _ = w.Write([]byte(body))
	}))
	t.Cleanup(srv.Close)
	return srv
}

func TestAConfirmedTokenPasses(t *testing.T) {
	srv := fakeCloudflare(t, 200, `{"success":true,"hostname":"1ms.my"}`, nil)
	if err := New("sec").WithEndpoint(srv.URL).Verify(context.Background(), "tok", ""); err != nil {
		t.Fatalf("want nil, got %v", err)
	}
}

func TestTheSecretAndTokenActuallyReachCloudflare(t *testing.T) {
	var seen http.Request
	srv := fakeCloudflare(t, 200, `{"success":true}`, &seen)

	err := New("the-secret").WithEndpoint(srv.URL).
		Verify(context.Background(), "the-token", "203.0.113.7")
	if err != nil {
		t.Fatalf("want nil, got %v", err)
	}
	if got := seen.PostForm.Get("secret"); got != "the-secret" {
		t.Errorf("secret = %q, want the-secret", got)
	}
	if got := seen.PostForm.Get("response"); got != "the-token" {
		t.Errorf("response = %q, want the-token", got)
	}
	if got := seen.PostForm.Get("remoteip"); got != "203.0.113.7" {
		t.Errorf("remoteip = %q, want 203.0.113.7", got)
	}
}

func TestAnEmptyRemoteIPIsOmittedRatherThanSentBlank(t *testing.T) {
	// Cloudflare rejects a blank remoteip, so it must be absent, not empty.
	var seen http.Request
	srv := fakeCloudflare(t, 200, `{"success":true}`, &seen)

	if err := New("s").WithEndpoint(srv.URL).Verify(context.Background(), "t", ""); err != nil {
		t.Fatalf("want nil, got %v", err)
	}
	if _, present := seen.PostForm["remoteip"]; present {
		t.Error("remoteip was sent even though no address was known")
	}
}

// -- the caller's fault: 403 ------------------------------------------------

func TestAMissingTokenIsRejectedWithoutAskingCloudflare(t *testing.T) {
	// A curl with no token is the overwhelmingly common case; spending a
	// round-trip on it would let anyone drive our egress traffic.
	called := false
	srv := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		called = true
	}))
	t.Cleanup(srv.Close)

	err := New("s").WithEndpoint(srv.URL).Verify(context.Background(), "", "")
	if !errors.Is(err, ErrRejected) {
		t.Fatalf("want ErrRejected, got %v", err)
	}
	if called {
		t.Error("an empty token still cost a request to Cloudflare")
	}
}

func TestWhitespaceIsNotAToken(t *testing.T) {
	err := New("s").WithEndpoint("http://127.0.0.1:1").Verify(context.Background(), "   ", "")
	if !errors.Is(err, ErrRejected) {
		t.Fatalf("want ErrRejected, got %v", err)
	}
}

func TestASpentOrForgedTokenIsRejected(t *testing.T) {
	srv := fakeCloudflare(t, 200,
		`{"success":false,"error-codes":["timeout-or-duplicate"]}`, nil)

	err := New("s").WithEndpoint(srv.URL).Verify(context.Background(), "replayed", "")
	if !errors.Is(err, ErrRejected) {
		t.Fatalf("want ErrRejected, got %v", err)
	}
	// The reason has to survive into the message or debugging is guesswork.
	if !strings.Contains(err.Error(), "timeout-or-duplicate") {
		t.Errorf("error lost Cloudflare's reason: %v", err)
	}
}

// -- not the caller's fault: must not read as a rejection -------------------

func TestOurOwnMisconfigurationIsNotBlamedOnTheCaller(t *testing.T) {
	// Reporting these as ErrRejected would tell every visitor their browser
	// failed a check it never ran, and would bury a broken deploy inside what
	// looks like ordinary abuse. invalid-input-secret in particular is exactly
	// what a placeholder left in Vault produces.
	for _, code := range []string{
		"internal-error",
		"missing-input-secret",
		"invalid-input-secret",
		"invalid-input-sitekey",
	} {
		srv := fakeCloudflare(t, 200,
			`{"success":false,"error-codes":["`+code+`"]}`, nil)

		err := New("s").WithEndpoint(srv.URL).Verify(context.Background(), "tok", "")
		if err == nil {
			t.Fatalf("%s: want an error", code)
		}
		if errors.Is(err, ErrRejected) {
			t.Errorf("%s was reported as a bad token: %v", code, err)
		}
	}
}

func TestTheCallersOwnMistakesStayTheCallers(t *testing.T) {
	// The other side of the same line: these must remain 403, or a real
	// abuse loop would read as a server fault and get retried politely.
	for _, code := range []string{
		"missing-input-response",
		"invalid-input-response",
		"timeout-or-duplicate",
		"bad-request",
	} {
		srv := fakeCloudflare(t, 200,
			`{"success":false,"error-codes":["`+code+`"]}`, nil)

		err := New("s").WithEndpoint(srv.URL).Verify(context.Background(), "tok", "")
		if !errors.Is(err, ErrRejected) {
			t.Errorf("%s: want ErrRejected, got %v", code, err)
		}
	}
}

func TestAnUnreachableCloudflareFailsClosed(t *testing.T) {
	// The whole point. If this ever returns nil, the endpoint is open again to
	// anyone who can make siteverify time out.
	err := New("s").WithEndpoint("http://127.0.0.1:1").Verify(context.Background(), "tok", "")
	if err == nil {
		t.Fatal("verification passed while Cloudflare was unreachable")
	}
	if errors.Is(err, ErrRejected) {
		t.Errorf("a transport failure was reported as a bad token: %v", err)
	}
}

func TestANonJSONReplyFailsClosed(t *testing.T) {
	srv := fakeCloudflare(t, 200, `<html>we are having a moment</html>`, nil)
	if err := New("s").WithEndpoint(srv.URL).Verify(context.Background(), "t", ""); err == nil {
		t.Fatal("verification passed on an unparseable reply")
	}
}

func TestAnHTTPErrorFromCloudflareFailsClosed(t *testing.T) {
	srv := fakeCloudflare(t, 502, `{"success":true}`, nil)
	// success:true in the body must not rescue a 502 — a proxy error page can
	// say anything.
	if err := New("s").WithEndpoint(srv.URL).Verify(context.Background(), "t", ""); err == nil {
		t.Fatal("verification passed on HTTP 502")
	}
}

// -- and the secret must not escape ----------------------------------------

func TestTheSecretNeverAppearsInAnError(t *testing.T) {
	// Errors reach slog, slog reaches Loki, and Loki answers unauthenticated
	// queries through a deliberately public Grafana.
	const secret = "s3cr3t-do-not-log-me"
	srv := fakeCloudflare(t, 500, `nope`, nil)

	for _, err := range []error{
		New(secret).WithEndpoint(srv.URL).Verify(context.Background(), "t", ""),
		New(secret).WithEndpoint("http://127.0.0.1:1").Verify(context.Background(), "t", ""),
		New(secret).WithEndpoint(srv.URL).Verify(context.Background(), "", ""),
	} {
		if err != nil && strings.Contains(err.Error(), secret) {
			t.Errorf("secret leaked into an error: %v", err)
		}
	}
}
