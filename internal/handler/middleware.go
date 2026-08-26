package handler

import (
	"net/http"
)

// methodHandler wraps an http.HandlerFunc and enforces the expected HTTP method.
// If the request uses a different method, respond with 405 Method Not Allowed.
// MethodHandler wraps an http.HandlerFunc and enforces the expected HTTP method.
// If the request uses a different method, respond with 405 Method Not Allowed.
func MethodHandler(method string, h http.HandlerFunc) http.HandlerFunc {
    return func(w http.ResponseWriter, r *http.Request) {
        if r.Method != method {
            w.Header().Set("Allow", method)
            http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
            return
        }
        h(w, r)
    }
}

// methodHandler is an unexported shim that delegates to the exported MethodHandler.
// Tests in the repository reference `methodHandler`; provide it for compatibility.
func methodHandler(method string, h http.HandlerFunc) http.HandlerFunc {
    return MethodHandler(method, h)
}
