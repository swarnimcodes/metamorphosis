// Package google verifies Google ID tokens (the `credential` the web button gives the browser, or the token a
// native Android client obtains).
//
// Only RS256 tokens signed by a key in Google's published key set are accepted, issued by Google, for one of our
// client ids, unexpired, with a verified email. Accounts are keyed on the stable `sub`, never on the email.
package google

import (
	"context"
	"crypto/rsa"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"math/big"
	"net/http"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/golang-jwt/jwt/v5"
)

const JWKSURL = "https://www.googleapis.com/oauth2/v3/certs"

var issuers = []string{"https://accounts.google.com", "accounts.google.com"}

const (
	clockSkew = 60 * time.Second
	// An unknown `kid` triggers at most one key refetch per interval, so junk tokens cannot make us hammer Google.
	minRefetch = 30 * time.Second
	defaultTTL = time.Hour
)

var (
	// ErrInvalid means the token is not acceptable: bad signature, audience, issuer, expiry...
	ErrInvalid = errors.New("invalid Google token")
	// ErrKeysUnavailable means we could not get Google's signing keys. Not the caller's fault.
	ErrKeysUnavailable = errors.New("could not fetch Google's signing keys")
)

type Identity struct {
	Sub     string
	Email   string
	Name    *string
	Picture *string
}

type keySet struct {
	keys      map[string]*rsa.PublicKey
	fetchedAt time.Time
	expiresAt time.Time
}

type Verifier struct {
	clientIDs []string
	url       string // empty: fixed keys, never refetched (tests)
	http      *http.Client

	mu    sync.Mutex
	cache *keySet
}

func NewRemote(clientIDs []string, url string) *Verifier {
	return &Verifier{clientIDs: clientIDs, url: url, http: &http.Client{Timeout: 5 * time.Second}}
}

// NewStatic builds a verifier with fixed keys, for tests: nothing touches the network.
func NewStatic(clientIDs []string, keys map[string]*rsa.PublicKey) *Verifier {
	now := time.Now()
	return &Verifier{clientIDs: clientIDs, cache: &keySet{keys: keys, fetchedAt: now, expiresAt: now.Add(10 * 365 * 24 * time.Hour)}}
}

type jwk struct {
	Kid string `json:"kid"`
	Kty string `json:"kty"`
	N   string `json:"n"`
	E   string `json:"e"`
}

func (v *Verifier) fetch(ctx context.Context) (*keySet, error) {
	unavailable := func(err error) (*keySet, error) {
		slog.Warn("fetching Google signing keys failed", "error", err)
		return nil, ErrKeysUnavailable
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, v.url, nil)
	if err != nil {
		return unavailable(err)
	}
	resp, err := v.http.Do(req)
	if err != nil {
		return unavailable(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode/100 != 2 {
		return unavailable(fmt.Errorf("status %d", resp.StatusCode))
	}
	ttl := defaultTTL
	for _, d := range strings.Split(resp.Header.Get("Cache-Control"), ",") {
		if secs, ok := strings.CutPrefix(strings.TrimSpace(d), "max-age="); ok {
			if n, err := strconv.ParseInt(secs, 10, 64); err == nil {
				ttl = time.Duration(min(max(n, 60), 86_400)) * time.Second
			}
			break
		}
	}
	var body struct {
		Keys []jwk `json:"keys"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		return unavailable(err)
	}
	keys := map[string]*rsa.PublicKey{}
	for _, k := range body.Keys {
		if pub := k.public(); pub != nil {
			keys[k.Kid] = pub
		}
	}
	now := time.Now()
	return &keySet{keys: keys, fetchedAt: now, expiresAt: now.Add(ttl)}, nil
}

func (k jwk) public() *rsa.PublicKey {
	if k.Kty != "RSA" {
		return nil
	}
	n, err1 := base64.RawURLEncoding.DecodeString(k.N)
	e, err2 := base64.RawURLEncoding.DecodeString(k.E)
	if err1 != nil || err2 != nil || len(n) == 0 || len(e) == 0 {
		return nil
	}
	return &rsa.PublicKey{N: new(big.Int).SetBytes(n), E: int(new(big.Int).SetBytes(e).Int64())}
}

// keyFor returns the key for kid, refetching Google's set when it has expired or Google may have rotated
// (an unknown kid, rate limited). Holding the lock across the fetch makes concurrent misses share one request.
func (v *Verifier) keyFor(ctx context.Context, kid string) (*rsa.PublicKey, error) {
	v.mu.Lock()
	defer v.mu.Unlock()
	now := time.Now()
	c := v.cache
	if c != nil && c.expiresAt.After(now) {
		if key, ok := c.keys[kid]; ok {
			return key, nil
		}
	}
	stale := c == nil || !c.expiresAt.After(now) || (c.keys[kid] == nil && now.Sub(c.fetchedAt) >= minRefetch)
	if stale && v.url != "" {
		fresh, err := v.fetch(ctx)
		if err != nil {
			return nil, err
		}
		v.cache, c = fresh, fresh
	}
	if c != nil {
		if key, ok := c.keys[kid]; ok {
			return key, nil
		}
	}
	return nil, ErrInvalid
}

func (v *Verifier) Verify(ctx context.Context, idToken string) (Identity, error) {
	parser := jwt.NewParser()
	unverified, _, err := parser.ParseUnverified(idToken, jwt.MapClaims{})
	if err != nil || unverified.Method.Alg() != "RS256" {
		return Identity{}, ErrInvalid
	}
	kid, _ := unverified.Header["kid"].(string)
	if kid == "" {
		return Identity{}, ErrInvalid
	}
	key, err := v.keyFor(ctx, kid)
	if err != nil {
		return Identity{}, err
	}

	claims := jwt.MapClaims{}
	if _, err := jwt.ParseWithClaims(idToken, claims, func(*jwt.Token) (any, error) { return key, nil },
		jwt.WithValidMethods([]string{"RS256"}), jwt.WithExpirationRequired(), jwt.WithLeeway(clockSkew)); err != nil {
		slog.Debug("Google ID token rejected", "error", err)
		return Identity{}, ErrInvalid
	}

	iss, _ := claims["iss"].(string)
	if !slices.Contains(issuers, iss) || !v.audienceOK(claims["aud"]) {
		return Identity{}, ErrInvalid
	}
	sub, _ := claims["sub"].(string)
	email, _ := claims["email"].(string)
	verified, _ := claims["email_verified"].(bool)
	if sub == "" || email == "" || !verified {
		return Identity{}, ErrInvalid
	}
	opt := func(k string) *string {
		if s, ok := claims[k].(string); ok {
			return &s
		}
		return nil
	}
	return Identity{Sub: sub, Email: email, Name: opt("name"), Picture: opt("picture")}, nil
}

func (v *Verifier) audienceOK(aud any) bool {
	switch a := aud.(type) {
	case string:
		return slices.Contains(v.clientIDs, a)
	case []any:
		for _, x := range a {
			if s, ok := x.(string); ok && slices.Contains(v.clientIDs, s) {
				return true
			}
		}
	}
	return false
}
