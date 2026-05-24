package cache

import (
	"context"
	"errors"
	"time"
)

// ErrNotFound is returned by Get when a key is not present.
var ErrNotFound = errors.New("cache: not found")

// Cache abstracts the read-through cache so we can swap implementations
// (NoopCache today; Redis or Dragonfly later) without touching handler code.
type Cache interface {
	// Get returns the cached value, or ErrNotFound if absent.
	Get(ctx context.Context, key string) (string, error)
	// Set stores a value with a TTL. Implementations may ignore TTL.
	Set(ctx context.Context, key, value string, ttl time.Duration) error
	// Ping verifies the cache is reachable.
	Ping(ctx context.Context) error
	// Close releases any underlying resources.
	Close() error
}
