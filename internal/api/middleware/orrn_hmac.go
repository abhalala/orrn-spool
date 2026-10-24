package middleware

import (
	"bytes"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"io"
	"strconv"
	"strings"
	"sync"
	"time"
)

// ORRN's SpoolClient (packages/server/src/lib/spool-crypto.ts, signSpoolRequest)
// signs every request with the node's shared secret:
//
//	Authorization: Bearer v1:<ts>:<sig>
//	ts  = Unix milliseconds in base 36
//	sig = hex(HMAC-SHA256(secret, METHOD "|" pathWithQuery "|" ts "|" bodyHash))
//	bodyHash = hex(SHA-256(body)), or "" when there is no body
//
// The spool accepts it alongside its own JWT. See docs/scale-and-orrn-auth.md.

const (
	// ORRNTokenPrefix starts every ORRN machine token.
	ORRNTokenPrefix = "v1:"
	// ORRNMaxSkew is the allowed clock difference either way.
	ORRNMaxSkew = 5 * time.Minute
	// orrnMaxBody caps how much body is buffered to hash.
	orrnMaxBody = 8 << 20
)

// Errors returned by Verify; the HTTP layer maps all of them to 401.
var (
	ErrORRNMalformed = errors.New("orrn token malformed")
	ErrORRNSkew      = errors.New("orrn token outside clock window")
	ErrORRNSignature = errors.New("orrn token signature mismatch")
	ErrORRNReplay    = errors.New("orrn token already used")
)

// SignORRN builds the token (without "Bearer ") for method, path and body at
// time now. The spool uses it for its heartbeat to ORRN; tests use it too.
func SignORRN(secret, method, path string, body []byte, now time.Time) string {
	ts := strconv.FormatInt(now.UnixMilli(), 36)
	return ORRNTokenPrefix + ts + ":" + orrnSignature(secret, method, path, ts, bodyHash(body))
}

func orrnSignature(secret, method, path, ts, bodyHashHex string) string {
	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write([]byte(strings.ToUpper(method) + "|" + path + "|" + ts + "|" + bodyHashHex))
	return hex.EncodeToString(mac.Sum(nil))
}

func bodyHash(body []byte) string {
	if len(body) == 0 {
		return ""
	}
	sum := sha256.Sum256(body)
	return hex.EncodeToString(sum[:])
}

// ORRNVerifier checks ORRN machine tokens and refuses a token seen before
// inside the skew window (replay).
type ORRNVerifier struct {
	secret string
	now    func() time.Time

	mu   sync.Mutex
	seen map[string]time.Time // signature -> forget after
}

// NewORRNVerifier returns nil when secret is empty (HMAC auth off).
func NewORRNVerifier(secret string) *ORRNVerifier {
	if strings.TrimSpace(secret) == "" {
		return nil
	}
	return &ORRNVerifier{secret: secret, now: time.Now, seen: map[string]time.Time{}}
}

// WithClock replaces time.Now (tests).
func (v *ORRNVerifier) WithClock(now func() time.Time) *ORRNVerifier {
	v.now = now
	return v
}

// Verify checks token for the request. path is the request URI (path plus
// "?query" when present), exactly as ORRN signed it.
func (v *ORRNVerifier) Verify(method, path string, body []byte, token string) error {
	if !strings.HasPrefix(token, ORRNTokenPrefix) {
		return ErrORRNMalformed
	}
	parts := strings.Split(token, ":")
	if len(parts) != 3 || parts[1] == "" || len(parts[2]) != sha256.Size*2 {
		return ErrORRNMalformed
	}
	ts, sig := parts[1], parts[2]
	ms, err := strconv.ParseInt(ts, 36, 64)
	if err != nil {
		return ErrORRNMalformed
	}
	now := v.now()
	skew := now.Sub(time.UnixMilli(ms))
	if skew > ORRNMaxSkew || skew < -ORRNMaxSkew {
		return ErrORRNSkew
	}
	got, err := hex.DecodeString(sig)
	if err != nil {
		return ErrORRNMalformed
	}
	ok := false
	// No body hashes as ""; a client that hashed an empty body is accepted too.
	candidates := []string{bodyHash(body)}
	if len(body) == 0 {
		empty := sha256.Sum256(nil)
		candidates = append(candidates, hex.EncodeToString(empty[:]))
	}
	for _, h := range candidates {
		want, _ := hex.DecodeString(orrnSignature(v.secret, method, path, ts, h))
		if hmac.Equal(got, want) {
			ok = true
		}
	}
	if !ok {
		return ErrORRNSignature
	}

	v.mu.Lock()
	defer v.mu.Unlock()
	for k, until := range v.seen {
		if now.After(until) {
			delete(v.seen, k)
		}
	}
	if _, dup := v.seen[sig]; dup {
		return ErrORRNReplay
	}
	v.seen[sig] = time.UnixMilli(ms).Add(ORRNMaxSkew)
	return nil
}

// readBody buffers the request body (bounded) and returns a fresh reader.
func readBody(r io.ReadCloser) ([]byte, io.ReadCloser, error) {
	if r == nil {
		return nil, io.NopCloser(bytes.NewReader(nil)), nil
	}
	defer r.Close()
	body, err := io.ReadAll(io.LimitReader(r, orrnMaxBody+1))
	if err != nil {
		return nil, nil, err
	}
	if len(body) > orrnMaxBody {
		return nil, nil, errors.New("body too large")
	}
	return body, io.NopCloser(bytes.NewReader(body)), nil
}
