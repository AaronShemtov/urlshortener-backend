package cache

import (
	"context"
	"time"
)

// NoopCache satisfies the Cache interface without storing anything.
// Every Get returns ErrNotFound (forcing a fallthrough to authoritative storage).
// Every Set is silently discarded. Ping always succeeds.
//
// This lets the MVP run without Redis. When Redis is added later, only the
// constructor call in main.go changes — handlers are oblivious.
type NoopCache struct{}

// NewNoopCache returns a cache that stores nothing.
func NewNoopCache() *NoopCache { return &NoopCache{} }

func (n *NoopCache) Get(_ context.Context, _ string) (string, error) {
	return "", ErrNotFound
}

func (n *NoopCache) Set(_ context.Context, _, _ string, _ time.Duration) error {
	return nil
}

func (n *NoopCache) Ping(_ context.Context) error { return nil }
func (n *NoopCache) Close() error                 { return nil }
