package handler

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/hex"
	"errors"
	"net"
	"net/http"
	"strings"

	"github.com/AaronShemtov/urlshortener-backend/internal/turnstile"
)

// ErrUnauthorized means the request offered no acceptable proof at all — no
// Turnstile token and no API key. Distinct from turnstile.ErrRejected, which
// means a token was offered and Cloudflare turned it down.
var ErrUnauthorized = errors.New("write refused: no Turnstile token and no API key")

// TokenHeader lets a caller pass the Turnstile token as a header instead of a
// JSON field. The widget names its form field cf-turnstile-response, so the
// header keeps that spelling.
const TokenHeader = "CF-Turnstile-Response"

// APIKeyHeader carries the shared key used by scripted callers.
const APIKeyHeader = "X-API-Key"

// WriteGate decides whether a write may proceed.
//
// Two proofs are accepted, because /shorten has two legitimate callers:
//
//   - a browser, which carries a Turnstile token minted by the widget on the
//     page. This is the path that closes the abuse: a token costs a real
//     browser solving a real challenge, so a loop cannot manufacture them.
//
//   - a script, which carries the API key. The homepage advertises
//     `curl -X POST 1ms.my/shorten`, and curl cannot solve a challenge, so
//     removing this path would break a documented feature rather than secure
//     it. The key is a secret, unlike the site key in the page.
//
// Either proof is sufficient; neither is optional.
type WriteGate struct {
	verifier *turnstile.Verifier
	apiKey   string
	open     bool
}

// NewWriteGate builds the production gate. verifier may be nil only if apiKey
// is set, and vice versa — a gate that can accept nothing would refuse every
// write, and a gate that requires nothing is the bug this type exists to stop.
func NewWriteGate(verifier *turnstile.Verifier, apiKey string) *WriteGate {
	return &WriteGate{verifier: verifier, apiKey: apiKey}
}

// NewOpenWriteGate allows every write without proof.
//
// Named this bluntly so that its presence anywhere outside a test is obvious
// in a diff. An unauthenticated /shorten is precisely how 6002 phishing links
// ended up in this database; it must never be the default that something gets
// by forgetting to pass a gate.
func NewOpenWriteGate() *WriteGate { return &WriteGate{open: true} }

// Authorize returns nil when the request may write. token is the Turnstile
// token taken from the request body; an empty string means the body carried
// none, in which case the header is consulted.
func (g *WriteGate) Authorize(ctx context.Context, r *http.Request, token string) error {
	if g.open {
		return nil
	}

	// The API key first: it costs a string compare, while the token costs a
	// round trip to Cloudflare.
	if g.apiKey != "" {
		if presented := r.Header.Get(APIKeyHeader); presented != "" {
			if subtle.ConstantTimeCompare([]byte(presented), []byte(g.apiKey)) == 1 {
				return nil
			}
			// A wrong key is a decision, not an invitation to try the other
			// door — returning here keeps a key-guessing loop from also
			// burning Cloudflare verifications.
			return ErrUnauthorized
		}
	}

	if token == "" {
		token = r.Header.Get(TokenHeader)
	}
	if g.verifier == nil {
		return ErrUnauthorized
	}
	return g.verifier.Verify(ctx, token, ClientIP(r))
}

// ClientIP returns the caller's address as Cloudflare reports it.
//
// RemoteAddr is useless here: every request arrives from the Envoy Gateway
// inside the cluster, so it is the same address for the whole internet.
// CF-Connecting-IP is set by Cloudflare and, because all traffic reaches this
// service through the tunnel, cannot be spoofed by the client — Cloudflare
// overwrites whatever the caller sent.
func ClientIP(r *http.Request) string {
	if ip := r.Header.Get("CF-Connecting-IP"); ip != "" {
		return strings.TrimSpace(ip)
	}
	if xff := r.Header.Get("X-Forwarded-For"); xff != "" {
		// Left-most entry is the original client; the rest are proxies.
		if comma := strings.IndexByte(xff, ','); comma >= 0 {
			return strings.TrimSpace(xff[:comma])
		}
		return strings.TrimSpace(xff)
	}
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		return r.RemoteAddr
	}
	return host
}

// refused reports whether the error is the caller's fault (403) rather than a
// verification service we could not reach (503).
func refused(err error) bool {
	return errors.Is(err, ErrUnauthorized) || errors.Is(err, turnstile.ErrRejected)
}

// fingerprintSalt is fresh per process and never leaves it, so a fingerprint
// cannot be turned back into an address by anyone reading the logs — including
// by us, later, with a list of candidate addresses.
var fingerprintSalt = func() []byte {
	b := make([]byte, 16)
	if _, err := rand.Read(b); err != nil {
		// A predictable salt is worse than none, because it looks anonymous
		// while being reversible by brute force over the IPv4 space.
		panic("cannot seed the client fingerprint salt: " + err.Error())
	}
	return b
}()

// clientFingerprint identifies a caller across requests without recording who
// they are.
//
// Logging the address itself is not an option here: these lines go to Loki,
// and Loki answers unauthenticated queries through a Grafana that is public on
// purpose. A refusal log full of visitor IP addresses would publish them.
//
// What the log actually needs to answer is "one source or many?", and a hash
// answers that exactly as well. It is deliberately per-process and unsalted by
// anything durable, so it is comparable within one pod's lifetime and
// meaningless outside it — enough to recognise a loop, useless as a record.
func clientFingerprint(r *http.Request) string {
	ip := ClientIP(r)
	if ip == "" {
		return "unknown"
	}
	sum := sha256.Sum256(append(append([]byte{}, fingerprintSalt...), ip...))
	return hex.EncodeToString(sum[:4])
}
