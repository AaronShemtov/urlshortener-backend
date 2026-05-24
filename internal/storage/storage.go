package storage

import (
	"context"
	"errors"
)

// ErrNotFound is returned by Get when the short code is not present.
var ErrNotFound = errors.New("storage: not found")

// ShortURL is the canonical record stored in the database.
type ShortURL struct {
	Code      string
	LongURL   string
	CreatedAt string
}

// Storage is the persistence layer interface. Reader and writer code both
// depend on this interface — provider-specific code (OCI NoSQL today,
// potentially something else tomorrow) lives in the implementation files.
type Storage interface {
	// SaveIfNotExists writes only if the code is not already taken.
	// Returns true if the row was inserted, false on collision.
	SaveIfNotExists(ctx context.Context, u ShortURL) (bool, error)

	// Get returns nil, ErrNotFound when the code is not found.
	Get(ctx context.Context, code string) (*ShortURL, error)

	// Ping verifies the storage is reachable. Used by readiness probe.
	Ping(ctx context.Context) error

	// Close releases any underlying resources.
	Close() error
}
