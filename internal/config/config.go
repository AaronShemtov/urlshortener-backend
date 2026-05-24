package config

import (
	"fmt"
	"os"
	"strconv"
)

// Config is loaded from environment variables at startup.
type Config struct {
	// Mode is the server role: "writer", "reader", or "all".
	// "all" is for local dev — registers both writer and reader endpoints.
	Mode string

	// Port the HTTP server listens on.
	Port string

	// BaseURL is the public origin used when constructing short URLs in
	// the writer response, e.g. "https://1ms.my".
	BaseURL string

	// ShortCodeLength is the length of generated short codes (3..32).
	ShortCodeLength int
}

// Load reads configuration from env vars and validates it.
func Load() (*Config, error) {
	cfg := &Config{
		Mode:            getEnv("MODE", "all"),
		Port:            getEnv("PORT", "8080"),
		BaseURL:         getEnv("BASE_URL", "https://1ms.my"),
		ShortCodeLength: getEnvInt("SHORT_CODE_LENGTH", 6),
	}

	switch cfg.Mode {
	case "writer", "reader", "all":
		// ok
	default:
		return nil, fmt.Errorf("invalid MODE %q (want writer|reader|all)", cfg.Mode)
	}

	if cfg.ShortCodeLength < 3 || cfg.ShortCodeLength > 32 {
		return nil, fmt.Errorf("SHORT_CODE_LENGTH must be 3..32, got %d", cfg.ShortCodeLength)
	}

	return cfg, nil
}

func getEnv(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}

func getEnvInt(key string, def int) int {
	if v := os.Getenv(key); v != "" {
		if i, err := strconv.Atoi(v); err == nil {
			return i
		}
	}
	return def
}
