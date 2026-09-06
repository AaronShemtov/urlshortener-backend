package handler

import (
	"net/http"
)

// MethodHandler wraps an http.HandlerFunc and enforces the expected HTTP method,
// answering 405 Method Not Allowed to anything else.
//
// A handler registered for GET also answers HEAD. HEAD is defined as GET without
// a response body, and net/http already discards the body for it, so the two
// cannot disagree — while refusing HEAD breaks callers that never wanted the body
// in the first place.
//
// That is not hypothetical here. Link previews in Slack, Telegram and the like
// fetch a URL with HEAD, and a shortener that answers 405 gives them nothing to
// unfurl. It went unnoticed because the smoke test that would have caught it was
// itself being turned away by Cloudflare before it ever reached this code.
func MethodHandler(method string, h http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if !methodAllowed(method, r.Method) {
			w.Header().Set("Allow", allowHeader(method))
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		h(w, r)
	}
}

func methodAllowed(expected, actual string) bool {
	if actual == expected {
		return true
	}
	return expected == http.MethodGet && actual == http.MethodHead
}

// allowHeader advertises everything that would in fact be accepted, because a
// 405 whose Allow header omits a method the server does accept is worse than
// no header at all.
func allowHeader(method string) string {
	if method == http.MethodGet {
		return "GET, HEAD"
	}
	return method
}

// methodHandler is an unexported shim that delegates to the exported MethodHandler.
// Tests in the repository reference `methodHandler`; provide it for compatibility.
func methodHandler(method string, h http.HandlerFunc) http.HandlerFunc {
	return MethodHandler(method, h)
}
