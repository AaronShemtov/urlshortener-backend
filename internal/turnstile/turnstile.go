// Package turnstile verifies Cloudflare Turnstile tokens.
//
// # Why this exists
//
// POST /shorten had no authentication of any kind. Between 2026-08-20 and
// 2026-08-22 a script used it to create 6002 short links pointing at 1001
// phishing pages impersonating a credit union, and 1ms.my served those
// redirects for the next sixteen days. 99.6% of the database was someone
// else's phishing campaign.
//
// Rate limiting cannot fix this. Cloudflare's free plan clamps the counting
// window to 10 seconds, so the strictest rule expressible — one request per
// ten seconds — still permits 8,640 writes a day from a single address.
//
// Turnstile changes what the caller must have rather than how often they may
// ask: a token that Cloudflare issued to a browser that passed a challenge.
// Tokens are single-use and expire in roughly five minutes, so they cannot be
// harvested and replayed in a loop.
//
// # On the two keys
//
// The site key embedded in the page is public by design — it names the widget
// and cannot mint tokens. The secret held here is what proves a verification
// came from us; it stays in the pod and is never sent to a browser.
package turnstile

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
)

// DefaultEndpoint is Cloudflare's verification endpoint.
const DefaultEndpoint = "https://challenges.cloudflare.com/turnstile/v0/siteverify"

// ErrRejected means Cloudflare positively declined the token: absent, malformed,
// already spent, expired, or issued for a different widget. It is the caller's
// fault, so handlers map it to 403.
var ErrRejected = errors.New("turnstile: token rejected")

// Verifier checks tokens against Cloudflare.
type Verifier struct {
	secret   string
	endpoint string
	client   *http.Client
}

// New returns a Verifier holding the widget's secret key.
func New(secret string) *Verifier {
	return &Verifier{
		secret:   secret,
		endpoint: DefaultEndpoint,
		// Short on purpose: this call sits in front of a user waiting for a
		// short link, and the server's own WriteTimeout is 10s.
		client: &http.Client{Timeout: 5 * time.Second},
	}
}

// WithEndpoint redirects verification at another URL. Tests use it to stand up
// a fake Cloudflare; production never calls it.
func (v *Verifier) WithEndpoint(endpoint string) *Verifier {
	v.endpoint = endpoint
	return v
}

// siteverifyResponse is Cloudflare's reply. Note the hyphen in error-codes —
// it is not a typo, that is the wire format.
type siteverifyResponse struct {
	Success     bool     `json:"success"`
	ErrorCodes  []string `json:"error-codes"`
	Hostname    string   `json:"hostname"`
	ChallengeTS string   `json:"challenge_ts"`
}

// Verify returns nil only when Cloudflare confirms the token. remoteIP is
// optional and may be empty; when supplied Cloudflare cross-checks it against
// the address that solved the challenge.
//
// This fails closed. If Cloudflare cannot be reached, Verify returns an error
// and the write is refused. Failing open would sell the endpoint for the price
// of a timeout, which is the same hole this package was written to close — and
// a shortener being briefly unavailable is a far smaller problem than a
// shortener quietly serving phishing again.
func (v *Verifier) Verify(ctx context.Context, token, remoteIP string) error {
	if strings.TrimSpace(token) == "" {
		return fmt.Errorf("%w: no token supplied", ErrRejected)
	}

	form := url.Values{
		"secret":   {v.secret},
		"response": {token},
	}
	if remoteIP != "" {
		form.Set("remoteip", remoteIP)
	}

	req, err := http.NewRequestWithContext(
		ctx, http.MethodPost, v.endpoint, strings.NewReader(form.Encode()),
	)
	if err != nil {
		return fmt.Errorf("turnstile: build request: %w", err)
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")

	resp, err := v.client.Do(req)
	if err != nil {
		return fmt.Errorf("turnstile: siteverify unreachable: %w", err)
	}
	defer func() {
		_, _ = io.Copy(io.Discard, resp.Body)
		_ = resp.Body.Close()
	}()

	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("turnstile: siteverify returned HTTP %d", resp.StatusCode)
	}

	var body siteverifyResponse
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		return fmt.Errorf("turnstile: decode siteverify response: %w", err)
	}

	if !body.Success {
		codes := strings.Join(body.ErrorCodes, ",")
		if codes == "" {
			codes = "no reason given"
		}
		// internal-error means Cloudflare failed, not the caller. Keeping it
		// distinct matters: the caller retrying is useless, and a spike of
		// these is our problem to notice, not evidence of an attack.
		for _, c := range body.ErrorCodes {
			if c == "internal-error" {
				return fmt.Errorf("turnstile: cloudflare internal error (%s)", codes)
			}
		}
		return fmt.Errorf("%w (%s)", ErrRejected, codes)
	}

	return nil
}
