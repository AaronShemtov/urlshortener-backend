package storage

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
)

// SodaStore talks to Oracle Autonomous Database via the SODA (Simple Oracle
// Document Access) REST API exposed by ORDS. Every operation is a plain
// HTTPS request — no Oracle client libraries, no wallet, no instance
// principal. Just Basic Auth over TLS.
//
// Trade-off vs. the OCI NoSQL SDK approach we used before:
//   - + same region as the cluster (~10ms vs. cross-region ~250ms)
//   - + zero compute cost on Always Free tier
//   - + no CGO, no Oracle binaries — Dockerfile and image size unchanged
//   - – credentials are static (in Vault, mounted as env) not workload-identity
//   - – no atomic put-if-absent; we emulate via read-then-write
//
// The race window in SaveIfNotExists is acceptable for this app: short codes
// are 6 random base62 chars (~56 billion combinations), the handler retries up
// to 5 times on collision, and the collision probability is essentially zero
// in any realistic deployment.
type SodaStore struct {
	// baseURL is the SODA REST root for the schema, with NO trailing slash.
	// Example: https://abc-foo.adb.il-jerusalem-1.oraclecloudapps.com/ords/admin/soda/latest
	baseURL string

	// collection is the SODA collection name (must already exist).
	collection string

	username string
	password string

	client *http.Client
}

// SodaConfig groups parameters for NewSodaStore; passing a struct rather
// than positional args keeps callers readable as we add knobs later
// (timeouts, custom transports, etc.).
type SodaConfig struct {
	BaseURL    string
	Collection string
	Username   string
	Password   string
	Timeout    time.Duration // per-request; default 10s if zero
}

// NewSodaStore is a constructor with no I/O — it can't fail, so it returns
// only the store. Connectivity is verified later via Ping() from the
// readiness probe. We do the same lazy-connect pattern as the old NoSQL
// storage so misconfigured creds don't crash the pod at startup; instead
// they show up as failed readiness probes, which is easier to diagnose.
func NewSodaStore(cfg SodaConfig) *SodaStore {
	timeout := cfg.Timeout
	if timeout == 0 {
		timeout = 10 * time.Second
	}
	return &SodaStore{
		baseURL:    strings.TrimRight(cfg.BaseURL, "/"),
		collection: cfg.Collection,
		username:   cfg.Username,
		password:   cfg.Password,
		client: &http.Client{
			Timeout: timeout,
		},
	}
}

// docURL builds the per-document REST endpoint:
//   {baseURL}/{collection}/{code}
// PathEscape protects against codes containing reserved characters, even
// though our generator only emits [0-9A-Za-z].
func (s *SodaStore) docURL(code string) string {
	return fmt.Sprintf("%s/%s/%s", s.baseURL, s.collection, url.PathEscape(code))
}

// collectionURL is the collection-level endpoint used by Ping.
func (s *SodaStore) collectionURL() string {
	return fmt.Sprintf("%s/%s/", s.baseURL, s.collection)
}

// SaveIfNotExists emulates put-if-absent on top of SODA's stateless REST API:
//   1) GET {code}  — does it exist?
//   2) If 404, PUT {code} with the document body.
//   3) Otherwise return (false, nil) to signal collision.
//
// There is a TOCTOU window between steps 1 and 2; see the type comment.
func (s *SodaStore) SaveIfNotExists(ctx context.Context, u ShortURL) (bool, error) {
	existing, err := s.Get(ctx, u.Code)
	if err == nil && existing != nil {
		return false, nil // collision
	}
	if err != nil && !errors.Is(err, ErrNotFound) {
		return false, fmt.Errorf("soda exists check: %w", err)
	}

	// The document payload — only the fields the application cares about.
	// SODA adds its own _id, version, lastModified, createdOn columns
	// automatically based on collection metadata we set at creation time.
	doc := map[string]string{
		"long_url":   u.LongURL,
		"created_at": u.CreatedAt,
	}
	body, err := json.Marshal(doc)
	if err != nil {
		return false, fmt.Errorf("marshal doc: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPut, s.docURL(u.Code), bytes.NewReader(body))
	if err != nil {
		return false, fmt.Errorf("build PUT: %w", err)
	}
	req.SetBasicAuth(s.username, s.password)
	req.Header.Set("Content-Type", "application/json")

	resp, err := s.client.Do(req)
	if err != nil {
		return false, fmt.Errorf("soda PUT: %w", err)
	}
	defer resp.Body.Close()

	// 200 = replace existing (shouldn't happen given the prior check, but
	// race-safe — we still consider it a successful save).
	// 201 = newly created (the normal case here).
	if resp.StatusCode != http.StatusOK && resp.StatusCode != http.StatusCreated {
		errBody, _ := io.ReadAll(resp.Body)
		return false, fmt.Errorf("soda PUT status %d: %s", resp.StatusCode, errBody)
	}
	return true, nil
}

// Get fetches one document by its client-assigned key.
//   - 200 → decode body and return *ShortURL
//   - 404 → return (nil, ErrNotFound)
//   - anything else → wrapped error
func (s *SodaStore) Get(ctx context.Context, code string) (*ShortURL, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, s.docURL(code), nil)
	if err != nil {
		return nil, fmt.Errorf("build GET: %w", err)
	}
	req.SetBasicAuth(s.username, s.password)

	resp, err := s.client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("soda GET: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode == http.StatusNotFound {
		return nil, ErrNotFound
	}
	if resp.StatusCode != http.StatusOK {
		errBody, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("soda GET status %d: %s", resp.StatusCode, errBody)
	}

	// The response body IS the JSON document we PUT earlier — SODA strips
	// the envelope when you GET by key (unlike when listing the collection).
	var doc struct {
		LongURL   string `json:"long_url"`
		CreatedAt string `json:"created_at"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&doc); err != nil {
		return nil, fmt.Errorf("decode soda response: %w", err)
	}
	return &ShortURL{
		Code:      code,
		LongURL:   doc.LongURL,
		CreatedAt: doc.CreatedAt,
	}, nil
}

// Ping verifies SODA REST is reachable and our credentials work. Used by the
// readiness probe — every periodSeconds the pod hits this against the
// collection root with limit=1 (smallest meaningful query). On a healthy
// network this returns in <50ms.
func (s *SodaStore) Ping(ctx context.Context) error {
	pingCtx, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()

	req, err := http.NewRequestWithContext(pingCtx, http.MethodGet, s.collectionURL()+"?limit=1", nil)
	if err != nil {
		return fmt.Errorf("build ping: %w", err)
	}
	req.SetBasicAuth(s.username, s.password)

	resp, err := s.client.Do(req)
	if err != nil {
		return fmt.Errorf("soda ping: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("soda ping status %d", resp.StatusCode)
	}
	return nil
}

// Close is a no-op for SodaStore beyond cleaning up idle TCP connections —
// http.Client has no Close method. Kept to satisfy the Storage interface.
func (s *SodaStore) Close() error {
	s.client.CloseIdleConnections()
	return nil
}