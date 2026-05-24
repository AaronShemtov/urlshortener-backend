package shortcode

import (
	"crypto/rand"
	"fmt"
	"math/big"
)

// URL-safe base62 alphabet. No '-', '_', '~', '/', '+' to avoid any URL
// escaping ambiguity. 62 characters gives ~5.95 bits of entropy per char,
// so length=6 yields 62^6 ≈ 56.8 billion combinations.
const charset = "abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789"

// Generate returns a cryptographically random short code of the given length.
//
// Uses crypto/rand (not math/rand) so codes can't be predicted from any seed —
// important when codes are the primary URL identifier.
//
// The naive `b[i] = charset[rng.Intn(62)]` approach with math/rand is unsafe
// (predictable). Using crypto/rand via math/big.Int avoids that AND avoids
// modulo bias from the obvious `byte % 62` trick.
func Generate(length int) (string, error) {
	if length < 1 {
		return "", fmt.Errorf("length must be >= 1, got %d", length)
	}

	result := make([]byte, length)
	max := big.NewInt(int64(len(charset)))

	for i := range result {
		n, err := rand.Int(rand.Reader, max)
		if err != nil {
			return "", fmt.Errorf("crypto rand: %w", err)
		}
		result[i] = charset[n.Int64()]
	}
	return string(result), nil
}
