package handler

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

func served(method, allow string) *httptest.ResponseRecorder {
	w := httptest.NewRecorder()
	MethodHandler(allow, func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Location", "https://example.com/target")
		w.WriteHeader(http.StatusMovedPermanently)
	})(w, httptest.NewRequest(method, "/abc123", nil))
	return w
}

func TestGetIsServed(t *testing.T) {
	if got := served(http.MethodGet, http.MethodGet).Code; got != http.StatusMovedPermanently {
		t.Fatalf("GET returned %d, want 301", got)
	}
}

func TestHeadIsServedByAGetHandler(t *testing.T) {
	// The bug this exists for: Slack, Telegram and other unfurlers fetch a link
	// with HEAD, and a shortener that answers 405 shows them nothing.
	w := served(http.MethodHead, http.MethodGet)
	if w.Code != http.StatusMovedPermanently {
		t.Fatalf("HEAD returned %d, want 301", w.Code)
	}
	if got := w.Header().Get("Location"); got != "https://example.com/target" {
		t.Errorf("HEAD lost the Location header: %q", got)
	}
}

func TestAnActuallyWrongMethodIsStillRefused(t *testing.T) {
	if got := served(http.MethodPost, http.MethodGet).Code; got != http.StatusMethodNotAllowed {
		t.Fatalf("POST to a GET handler returned %d, want 405", got)
	}
}

func TestHeadIsNotSmuggledIntoAPostHandler(t *testing.T) {
	// The concession is HEAD-for-GET and nothing else. A HEAD that reached a
	// write endpoint would run it and return no body to say so.
	if got := served(http.MethodHead, http.MethodPost).Code; got != http.StatusMethodNotAllowed {
		t.Fatalf("HEAD to a POST handler returned %d, want 405", got)
	}
}

func TestTheAllowHeaderNamesEverythingAccepted(t *testing.T) {
	// A 405 that omits a method the server does accept sends the caller looking
	// for a problem that is not there.
	if got := served(http.MethodPost, http.MethodGet).Header().Get("Allow"); got != "GET, HEAD" {
		t.Errorf("Allow = %q, want \"GET, HEAD\"", got)
	}
	if got := served(http.MethodGet, http.MethodPost).Header().Get("Allow"); got != "POST" {
		t.Errorf("Allow = %q, want \"POST\"", got)
	}
}
