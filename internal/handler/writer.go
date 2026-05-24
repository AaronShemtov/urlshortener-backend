package handler

import (
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"time"

	"github.com/AaronShemtov/urlshortener-backend/internal/cache"
	"github.com/AaronShemtov/urlshortener-backend/internal/shortcode"
	"github.com/AaronShemtov/urlshortener-backend/internal/storage"
)

// cacheTTL is how long write-through entries live in cache.
// 24h is a reasonable trade-off: hot links stay cached, but stale entries
// (deleted long URLs etc) get refreshed daily.
const cacheTTL = 24 * time.Hour

// WriterHandler serves the write endpoints. Only registered when MODE=writer or all.
type WriterHandler struct {
	storage    storage.Storage
	cache      cache.Cache
	baseURL    string
	codeLength int
}

func NewWriterHandler(s storage.Storage, c cache.Cache, baseURL string, codeLength int) *WriterHandler {
	return &WriterHandler{
		storage:    s,
		cache:      c,
		baseURL:    baseURL,
		codeLength: codeLength,
	}
}

type shortenRequest struct {
	URL  string `json:"url"`
	Code string `json:"code,omitempty"` // optional, used by CreateCustom
}

type shortenResponse struct {
	ShortURL string `json:"short_url"`
}

// Shorten allocates a random code and saves the mapping.
// Retries up to maxAttempts times on collision (extremely rare for 6+ char codes).
func (h *WriterHandler) Shorten(w http.ResponseWriter, r *http.Request) {
	var req shortenRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid JSON")
		return
	}
	if req.URL == "" {
		writeError(w, http.StatusBadRequest, "missing url")
		return
	}

	const maxAttempts = 5
	for attempt := 1; attempt <= maxAttempts; attempt++ {
		code, err := shortcode.Generate(h.codeLength)
		if err != nil {
			slog.ErrorContext(r.Context(), "generate short code", "error", err)
			writeError(w, http.StatusInternalServerError, "internal error")
			return
		}

		saved, err := h.storage.SaveIfNotExists(r.Context(), storage.ShortURL{
			Code:      code,
			LongURL:   req.URL,
			CreatedAt: time.Now().UTC().Format(time.RFC3339),
		})
		if err != nil {
			slog.ErrorContext(r.Context(), "storage save", "error", err)
			writeError(w, http.StatusInternalServerError, "storage error")
			return
		}

		if saved {
			// Write-through: pre-warm the cache so the first redirect doesn't
			// hit NoSQL. Cache failure is non-fatal — log and continue.
			if err := h.cache.Set(r.Context(), cacheKey(code), req.URL, cacheTTL); err != nil {
				slog.WarnContext(r.Context(), "cache pre-warm failed", "error", err)
			}
			writeJSON(w, http.StatusOK, shortenResponse{
				ShortURL: fmt.Sprintf("%s/%s", h.baseURL, code),
			})
			return
		}
		slog.InfoContext(r.Context(), "short code collision", "code", code, "attempt", attempt)
	}

	// 5 collisions in a row means RNG is broken or table is nearly full.
	// At 6-char base62 codes, 5 collisions has probability ~0 in practice.
	writeError(w, http.StatusInternalServerError, "failed to allocate short code")
}

// CreateCustom lets the caller specify the short code directly.
// Returns 409 Conflict if the code is taken.
func (h *WriterHandler) CreateCustom(w http.ResponseWriter, r *http.Request) {
	var req shortenRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid JSON")
		return
	}
	if req.URL == "" || req.Code == "" {
		writeError(w, http.StatusBadRequest, "missing url or code")
		return
	}
	if len(req.Code) < 4 {
		writeError(w, http.StatusBadRequest, "custom code must be at least 4 characters")
		return
	}

	saved, err := h.storage.SaveIfNotExists(r.Context(), storage.ShortURL{
		Code:      req.Code,
		LongURL:   req.URL,
		CreatedAt: time.Now().UTC().Format(time.RFC3339),
	})
	if err != nil {
		slog.ErrorContext(r.Context(), "storage save", "error", err)
		writeError(w, http.StatusInternalServerError, "storage error")
		return
	}
	if !saved {
		writeError(w, http.StatusConflict, "code already in use")
		return
	}

	if err := h.cache.Set(r.Context(), cacheKey(req.Code), req.URL, cacheTTL); err != nil {
		slog.WarnContext(r.Context(), "cache pre-warm failed", "error", err)
	}

	writeJSON(w, http.StatusOK, shortenResponse{
		ShortURL: fmt.Sprintf("%s/%s", h.baseURL, req.Code),
	})
}
