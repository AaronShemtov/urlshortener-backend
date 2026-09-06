package config

import (
	"strings"
	"testing"
)

// valid returns a config that passes, so each test can break exactly one thing.
func valid() *Config {
	return &Config{
		Mode:            "writer",
		Port:            "8080",
		BaseURL:         "https://1ms.my",
		ShortCodeLength: 6,
		ADBBaseURL:      "https://example.oraclecloudapps.com/ords/admin/soda/latest",
		ADBCollection:   "urls",
		ADBUsername:     "ADMIN",
		ADBPassword:     "pw",
		TurnstileSecret: "turnstile-secret",
	}
}

func TestTheBaselineConfigIsValid(t *testing.T) {
	if err := valid().Validate(); err != nil {
		t.Fatalf("baseline should validate, got %v", err)
	}
}

// -- the rule that exists because of the incident ---------------------------

func TestWriteModeWithNoAuthorisationRefusesToStart(t *testing.T) {
	// An unauthenticated /shorten is not a degraded mode. Starting anyway is
	// what let 6002 phishing links into the database in August, so the process
	// must die at boot instead of serving traffic.
	for _, mode := range []string{"writer", "all"} {
		c := valid()
		c.Mode = mode
		c.TurnstileSecret = ""
		c.APIKey = ""

		err := c.Validate()
		if err == nil {
			t.Fatalf("mode %q validated with neither TURNSTILE_SECRET nor SHORTEN_API_KEY", mode)
		}
		// The message has to name the fix, because whoever meets it is
		// looking at a pod that will not start.
		for _, want := range []string{"TURNSTILE_SECRET", "SHORTEN_API_KEY"} {
			if !strings.Contains(err.Error(), want) {
				t.Errorf("mode %q: error does not mention %s: %v", mode, want, err)
			}
		}
	}
}

func TestEitherProofAloneIsEnough(t *testing.T) {
	// Turnstile alone serves the web form; the API key alone serves scripts.
	// Requiring both would break one caller for no gain.
	cases := map[string]func(*Config){
		"turnstile only": func(c *Config) { c.TurnstileSecret = "s"; c.APIKey = "" },
		"api key only":   func(c *Config) { c.TurnstileSecret = ""; c.APIKey = "k" },
		"both":           func(c *Config) { c.TurnstileSecret = "s"; c.APIKey = "k" },
	}
	for name, mutate := range cases {
		c := valid()
		mutate(c)
		if err := c.Validate(); err != nil {
			t.Errorf("%s: want valid, got %v", name, err)
		}
	}
}

func TestReaderModeNeedsNoWriteAuthorisation(t *testing.T) {
	// Reader pods never write, and demanding a secret they cannot use would
	// mean putting it in a pod that has no business holding it.
	c := valid()
	c.Mode = "reader"
	c.TurnstileSecret = ""
	c.APIKey = ""
	if err := c.Validate(); err != nil {
		t.Fatalf("reader mode should not require write authorisation, got %v", err)
	}
}

// -- the pre-existing rules, pinned so the new clause did not displace them --

func TestTheOtherRequirementsStillApply(t *testing.T) {
	cases := map[string]func(*Config){
		"bad mode":        func(c *Config) { c.Mode = "sideways" },
		"code too short":  func(c *Config) { c.ShortCodeLength = 2 },
		"code too long":   func(c *Config) { c.ShortCodeLength = 33 },
		"no adb url":      func(c *Config) { c.ADBBaseURL = "" },
		"no adb user":     func(c *Config) { c.ADBUsername = "" },
		"no adb password": func(c *Config) { c.ADBPassword = "" },
	}
	for name, mutate := range cases {
		c := valid()
		mutate(c)
		if err := c.Validate(); err == nil {
			t.Errorf("%s: validated when it should not have", name)
		}
	}
}
