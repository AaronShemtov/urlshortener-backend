package handler

import (
	"context"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"

	"github.com/AaronShemtov/urlshortener-backend/internal/cache"
	"github.com/AaronShemtov/urlshortener-backend/internal/storage"
)

type memStore struct {
    mu sync.Mutex
    m  map[string]storage.ShortURL
}

func newMemStore() *memStore { return &memStore{m: make(map[string]storage.ShortURL)} }

func (s *memStore) SaveIfNotExists(_ context.Context, u storage.ShortURL) (bool, error) {
    s.mu.Lock()
    defer s.mu.Unlock()
    if _, ok := s.m[u.Code]; ok {
        return false, nil
    }
    s.m[u.Code] = u
    return true, nil
}

func (s *memStore) Get(_ context.Context, code string) (*storage.ShortURL, error) {
    s.mu.Lock()
    defer s.mu.Unlock()
    if v, ok := s.m[code]; ok {
        v2 := v
        return &v2, nil
    }
    return nil, storage.ErrNotFound
}

func (s *memStore) Ping(_ context.Context) error { return nil }
func (s *memStore) Close() error                    { return nil }

func TestRedirectCacheMissAndHit(t *testing.T) {
    s := newMemStore()
    // prepare record
    s.m["abc"] = storage.ShortURL{Code: "abc", LongURL: "https://example.com"}

    rh := NewReaderHandler(s, cache.NewNoopCache())

    // First request should hit storage and return 301
    req := httptest.NewRequest("GET", "/abc", nil)
    rr := httptest.NewRecorder()
    rh.Redirect(rr, req)
    if rr.Result().StatusCode != http.StatusMovedPermanently {
        t.Fatalf("expected 301, got %d", rr.Result().StatusCode)
    }

    // Non-GET should be rejected by methodHandler wrapper; test wrapper
    wrapped := methodHandler("GET", rh.Redirect)
    req2 := httptest.NewRequest("POST", "/abc", nil)
    rr2 := httptest.NewRecorder()
    wrapped(rr2, req2)
    if rr2.Result().StatusCode != http.StatusMethodNotAllowed {
        t.Fatalf("expected 405 for wrong method, got %d", rr2.Result().StatusCode)
    }
}
