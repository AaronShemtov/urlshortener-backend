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

	// NoSQLEndpoint is the OCI NoSQL Database Cloud Service endpoint,
	// e.g. "https://nosql.il-jerusalem-1.oci.oraclecloud.com".
	NoSQLEndpoint string

	// NoSQLTable is the name of the NoSQL table holding URL records.
	NoSQLTable string

	// OCICompartmentOCID is the compartment that owns the NoSQL table.
	// Instance Principal auth requires this to scope API calls.
	OCICompartmentOCID string
}

// Load reads configuration from env vars and validates it.
func Load() (*Config, error) {
	cfg := &Config{
		Mode:               getEnv("MODE", "all"),
		Port:               getEnv("PORT", "8080"),
		BaseURL:            getEnv("BASE_URL", "https://1ms.my"),
		ShortCodeLength:    getEnvInt("SHORT_CODE_LENGTH", 6),
		NoSQLEndpoint:      getEnv("NOSQL_ENDPOINT", ""),
		NoSQLTable:         getEnv("NOSQL_TABLE", "urls"),
		OCICompartmentOCID: getEnv("OCI_COMPARTMENT_OCID", ""),
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

	if c.NoSQLEndpoint == "" {
		return fmt.Errorf("NOSQL_ENDPOINT is required " +
			"(e.g. https://nosql.il-jerusalem-1.oci.oraclecloud.com)")
	}

	if c.OCICompartmentOCID == "" {
		return fmt.Errorf("OCI_COMPARTMENT_OCID is required for Instance Principal auth")
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