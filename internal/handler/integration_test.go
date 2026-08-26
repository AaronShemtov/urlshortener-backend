package handler

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"

	"github.com/AaronShemtov/urlshortener-backend/internal/cache"
	"github.com/AaronShemtov/urlshortener-backend/internal/storage"
)

type memStoreAll struct {
    mu sync.Mutex
    m  map[string]storage.ShortURL
}

func newMemStoreAll() *memStoreAll { return &memStoreAll{m: make(map[string]storage.ShortURL)} }

func (s *memStoreAll) SaveIfNotExists(_ context.Context, u storage.ShortURL) (bool, error) {
    s.mu.Lock()
    defer s.mu.Unlock()
    if _, ok := s.m[u.Code]; ok {
        return false, nil
    }
    s.m[u.Code] = u
    return true, nil
}
func (s *memStoreAll) Get(_ context.Context, code string) (*storage.ShortURL, error) {
    s.mu.Lock()
    defer s.mu.Unlock()
    if v, ok := s.m[code]; ok {
        v2 := v
        return &v2, nil
    }
    return nil, storage.ErrNotFound
}
func (s *memStoreAll) Ping(_ context.Context) error { return nil }
func (s *memStoreAll) Close() error                    { return nil }

func TestEndToEndShortenRedirect(t *testing.T) {
    s := newMemStoreAll()
    cacheClient := cache.NewNoopCache()
    wh := NewWriterHandler(s, cacheClient, "http://base", 6)
    rh := NewReaderHandler(s, cacheClient)

    mux := http.NewServeMux()
    mux.HandleFunc("/shorten", methodHandler("POST", wh.Shorten))
    mux.HandleFunc("/", methodHandler("GET", rh.Redirect))

    ts := httptest.NewServer(mux)
    defer ts.Close()

    // POST /shorten
    reqBody := map[string]string{"url": "https://example.com"}
    b, _ := json.Marshal(reqBody)
    resp, err := http.Post(ts.URL+"/shorten", "application/json", bytes.NewReader(b))
    if err != nil {
        t.Fatalf("post shorten error: %v", err)
    }
    if resp.StatusCode != http.StatusOK {
        t.Fatalf("expected 200 from shorten, got %d", resp.StatusCode)
    }
    var out map[string]string
    _ = json.NewDecoder(resp.Body).Decode(&out)
    resp.Body.Close()
    shortURL := out["short_url"]
    if shortURL == "" {
        t.Fatalf("short_url empty")
    }

    // Extract code from returned shortURL (last path element)
    // simple approach: find last '/'
    idx := -1
    for i := len(shortURL) - 1; i >= 0; i-- {
        if shortURL[i] == '/' {
            idx = i
            break
        }
    }
    if idx == -1 || idx == len(shortURL)-1 {
        t.Fatalf("invalid short url: %s", shortURL)
    }
    code := shortURL[idx+1:]

    // GET /{code} without following redirects
    client := &http.Client{
        CheckRedirect: func(req *http.Request, via []*http.Request) error {
            return http.ErrUseLastResponse
        },
    }
    gresp, err := client.Get(ts.URL + "/" + code)
    if err != nil {
        t.Fatalf("get redirect error: %v", err)
    }
    if gresp.StatusCode != http.StatusMovedPermanently {
        bts, _ := io.ReadAll(gresp.Body)
        gresp.Body.Close()
        t.Fatalf("expected 301, got %d, body: %s", gresp.StatusCode, string(bts))
    }
    loc := gresp.Header.Get("Location")
    if loc != "https://example.com" {
        t.Fatalf("unexpected redirect location: %s", loc)
    }
}
