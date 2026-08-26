package handler

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"

	"github.com/AaronShemtov/urlshortener-backend/internal/cache"
	"github.com/AaronShemtov/urlshortener-backend/internal/storage"
)

type memStoreW struct {
	mu sync.Mutex
	m  map[string]storage.ShortURL
}

func newMemStoreW() *memStoreW { return &memStoreW{m: make(map[string]storage.ShortURL)} }

func (s *memStoreW) SaveIfNotExists(_ context.Context, u storage.ShortURL) (bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, ok := s.m[u.Code]; ok {
		return false, nil
	}
	s.m[u.Code] = u
	return true, nil
}
func (s *memStoreW) Get(_ context.Context, code string) (*storage.ShortURL, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if v, ok := s.m[code]; ok {
		v2 := v
		return &v2, nil
	}
	return nil, storage.ErrNotFound
}
func (s *memStoreW) Ping(_ context.Context) error { return nil }
func (s *memStoreW) Close() error                 { return nil }

func TestShortenAndCreateCustom(t *testing.T) {
	s := newMemStoreW()
	wh := NewWriterHandler(s, cache.NewNoopCache(), "http://base", 6)

	// Test Shorten happy path
	reqBody := map[string]string{"url": "https://example.com"}
	b, _ := json.Marshal(reqBody)
	req := httptest.NewRequest("POST", "/shorten", bytes.NewReader(b))
	rr := httptest.NewRecorder()
	wh.Shorten(rr, req)
	if rr.Result().StatusCode != http.StatusOK {
		t.Fatalf("expected 200, got %d", rr.Result().StatusCode)
	}
	var resp map[string]string
	if err := json.NewDecoder(rr.Body).Decode(&resp); err != nil {
		t.Fatalf("invalid response json: %v", err)
	}
	if resp["short_url"] == "" {
		t.Fatalf("expected short_url in response")
	}

	// Test CreateCustom happy path
	reqBody2 := map[string]string{"url": "https://x.com", "code": "cust1"}
	b2, _ := json.Marshal(reqBody2)
	req2 := httptest.NewRequest("POST", "/createcustom", bytes.NewReader(b2))
	rr2 := httptest.NewRecorder()
	wh.CreateCustom(rr2, req2)
	if rr2.Result().StatusCode != http.StatusOK {
		t.Fatalf("expected 200 for create custom, got %d", rr2.Result().StatusCode)
	}

	// Test CreateCustom conflict
	rr3 := httptest.NewRecorder()
	req3 := httptest.NewRequest("POST", "/createcustom", bytes.NewReader(b2))
	wh.CreateCustom(rr3, req3)
	if rr3.Result().StatusCode != http.StatusConflict {
		t.Fatalf("expected 409 for duplicate code, got %d", rr3.Result().StatusCode)
	}
}

func TestCreateCustomValidation(t *testing.T) {
	tests := []struct {
		name       string
		code       string
		wantStatus int
		wantCode   string
	}{
		{name: "valid lowercase code", code: "abcd", wantStatus: http.StatusOK, wantCode: "abcd"},
		{name: "mixed case is lowercased", code: "AbCd", wantStatus: http.StatusOK, wantCode: "abcd"},
		{name: "too short", code: "abc", wantStatus: http.StatusBadRequest},
		{name: "invalid characters", code: "a$b#", wantStatus: http.StatusBadRequest},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			s := newMemStoreW()
			wh := NewWriterHandler(s, cache.NewNoopCache(), "http://base", 6)
			body, err := json.Marshal(map[string]string{
				"url":  "https://example.com",
				"code": tt.code,
			})
			if err != nil {
				t.Fatalf("marshal request: %v", err)
			}

			rr := httptest.NewRecorder()
			req := httptest.NewRequest("POST", "/createcustom", bytes.NewReader(body))
			wh.CreateCustom(rr, req)
			if rr.Result().StatusCode != tt.wantStatus {
				t.Fatalf("expected %d, got %d", tt.wantStatus, rr.Result().StatusCode)
			}
			if tt.wantCode != "" {
				stored, ok := s.m[tt.wantCode]
				if !ok || stored.Code != tt.wantCode {
					t.Fatalf("expected stored code %q", tt.wantCode)
				}
			}
		})
	}
}
