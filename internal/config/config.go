package config

import (
	"errors"
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

	// ADBBaseURL is the SODA REST root for the schema, no trailing slash.
	// Example:
	//   https://abc-foo.adb.il-jerusalem-1.oraclecloudapps.com/ords/admin/soda/latest
	ADBBaseURL string

	// ADBCollection is the name of the SODA collection holding URL records.
	ADBCollection string

	// ADBUsername is the database user. For Always Free ATP we use ADMIN
	// because REST-enabling a separate schema is blocked on managed
	// instances. Not ideal for production; acceptable for a personal app.
	ADBUsername string

	// ADBPassword is the database password. Injected from a k8s Secret
	// sourced from OCI Vault via ESO.
	ADBPassword string

	// TurnstileSecret is the Cloudflare Turnstile secret key. It verifies the
	// one-time tokens the widget on the page hands to a browser, and is what
	// authorises writes from the web form. Never sent to a client — the site
	// key in the page is the public half of the pair.
	TurnstileSecret string

	// APIKey authorises scripted callers, which cannot solve a challenge. The
	// homepage documents `curl -X POST 1ms.my/shorten`, so this is a feature
	// rather than a back door — but unlike the site key it is a secret.
	APIKey string
}

// Load reads configuration from env vars and validates it.
func Load() (*Config, error) {
	cfg := &Config{
		Mode:            getEnv("MODE", "all"),
		Port:            getEnv("PORT", "8080"),
		BaseURL:         getEnv("BASE_URL", "https://1ms.my"),
		ShortCodeLength: getEnvInt("SHORT_CODE_LENGTH", 6),
		ADBBaseURL:      getEnv("ADB_BASE_URL", ""),
		ADBCollection:   getEnv("ADB_COLLECTION", "urls"),
		ADBUsername:     getEnv("ADB_USERNAME", ""),
		ADBPassword:     getEnv("ADB_PASSWORD", ""),
		TurnstileSecret: getEnv("TURNSTILE_SECRET", ""),
		APIKey:          getEnv("SHORTEN_API_KEY", ""),
	}

	if err := cfg.Validate(); err != nil {
		return nil, err
	}
	return cfg, nil
}

// Validate is exported so it can be re-run after a CLI flag overrides a field
// (e.g. --mode=writer overwrites Mode after env load, then we re-validate).
func (c *Config) Validate() error {
	switch c.Mode {
	case "writer", "reader", "all":
		// ok
	default:
		return fmt.Errorf("invalid MODE %q (want writer|reader|all)", c.Mode)
	}

	if c.ShortCodeLength < 3 || c.ShortCodeLength > 32 {
		return fmt.Errorf("SHORT_CODE_LENGTH must be 3..32, got %d", c.ShortCodeLength)
	}

	if c.ADBBaseURL == "" {
		return errors.New("ADB_BASE_URL is required " +
			"(e.g. https://...oraclecloudapps.com/ords/admin/soda/latest)")
	}

	if c.ADBUsername == "" {
		return errors.New("ADB_USERNAME is required")
	}

	if c.ADBPassword == "" {
		return errors.New("ADB_PASSWORD is required")
	}

	// Refusing to start beats starting insecure. An unauthenticated /shorten
	// is not a degraded mode — between 2026-08-20 and 2026-08-22 it was used
	// to plant 6002 links to phishing pages, which this domain then served for
	// sixteen days. If neither proof is configured there is nothing to check,
	// so the process should die loudly at boot rather than pass traffic.
	if c.Mode == "writer" || c.Mode == "all" {
		if c.TurnstileSecret == "" && c.APIKey == "" {
			return errors.New("write mode requires TURNSTILE_SECRET (for the web form) " +
				"and/or SHORTEN_API_KEY (for scripted callers); refusing to serve an " +
				"unauthenticated /shorten")
		}
	}

	return nil
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
