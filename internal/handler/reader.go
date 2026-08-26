package handler

import (
	"errors"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"github.com/AaronShemtov/urlshortener-backend/internal/cache"
	"github.com/AaronShemtov/urlshortener-backend/internal/storage"
)

// ReaderHandler serves the redirect endpoint. Only registered when MODE=reader or all.
type ReaderHandler struct {
	storage storage.Storage
	cache   cache.Cache
}

func NewReaderHandler(s storage.Storage, c cache.Cache) *ReaderHandler {
	return &ReaderHandler{
		storage: s,
		cache:   c,
	}
}

// Redirect resolves a short code and 301s to the long URL.
//
// Cache-aside pattern:
//  1. Check cache. Hit -> redirect immediately (fast path).
//  2. Miss -> read from storage (authoritative).
//  3. Populate cache for next time.
//  4. Storage miss -> 404.
//
// Cache outage is non-fatal: handler keeps working through storage, just slower.
// Storage outage IS fatal for this request — we return 500.
func (h *ReaderHandler) Redirect(w http.ResponseWriter, r *http.Request) {
	// Extract the short code from the URL path. Example: /abc123 -> abc123
	code := strings.TrimPrefix(r.URL.Path, "/")
	if code == "" {
		http.NotFound(w, r)
		return
	}

	// 1. Cache lookup — fast path for hot links.
	if longURL, err := h.cache.Get(r.Context(), cacheKey(code)); err == nil {
		slog.DebugContext(r.Context(), "cache hit", "code", code)
		http.Redirect(w, r, longURL, http.StatusMovedPermanently)
		return
	} else if !errors.Is(err, cache.ErrNotFound) {
		// Cache outage — log but continue, storage is still authoritative.
		slog.WarnContext(r.Context(), "cache lookup failed", "code", code, "error", err)
	}

	// 2. Storage fallthrough.
	u, err := h.storage.Get(r.Context(), code)
	if err != nil {
		if errors.Is(err, storage.ErrNotFound) {
			http.NotFound(w, r)
			return
		}
		slog.ErrorContext(r.Context(), "storage lookup", "code", code, "error", err)
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}

	// 3. Populate cache so the next request takes the fast path.
	if err := h.cache.Set(r.Context(), cacheKey(code), u.LongURL, 24*time.Hour); err != nil {
		slog.WarnContext(r.Context(), "cache populate failed", "code", code, "error", err)
	}

	http.Redirect(w, r, u.LongURL, http.StatusMovedPermanently)
}
