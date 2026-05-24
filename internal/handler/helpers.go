package handler

import (
	"encoding/json"
	"net/http"
)

// cacheKey namespaces cache entries so this app can share a Redis with others
// later without key collisions.
func cacheKey(code string) string { return "url:" + code }

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

func writeError(w http.ResponseWriter, status int, msg string) {
	writeJSON(w, status, map[string]string{"error": msg})
}
