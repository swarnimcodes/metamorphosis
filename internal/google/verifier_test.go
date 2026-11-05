package google

import (
	"context"
	"crypto/rand"
	"crypto/rsa"
	"encoding/base64"
	"encoding/json"
	"errors"
	"math/big"
	"net/http"
	"net/http/httptest"
	"os"
	"sync/atomic"
	"testing"
	"time"

	"github.com/golang-jwt/jwt/v5"
)

const testClient = "web.apps.googleusercontent.com"

func newKey(t *testing.T) *rsa.PrivateKey {
	t.Helper()
	k, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	return k
}

func jwkOf(kid string, k *rsa.PrivateKey) map[string]string {
	b64 := base64.RawURLEncoding.EncodeToString
	return map[string]string{
		"kty": "RSA", "alg": "RS256", "use": "sig", "kid": kid,
		"n": b64(k.N.Bytes()), "e": b64(big.NewInt(int64(k.E)).Bytes()),
	}
}

func token(t *testing.T, kid string, k *rsa.PrivateKey) string {
	t.Helper()
	tok := jwt.NewWithClaims(jwt.SigningMethodRS256, jwt.MapClaims{
		"iss": "https://accounts.google.com", "aud": testClient, "sub": "g-1", "email": "a@example.com",
		"email_verified": true, "exp": time.Now().Add(time.Hour).Unix(),
	})
	tok.Header["kid"] = kid
	s, err := tok.SignedString(k)
	if err != nil {
		t.Fatal(err)
	}
	return s
}

// keyServer serves a JWKS and counts the requests it gets. keys and status can be changed between requests.
type keyServer struct {
	*httptest.Server
	hits   atomic.Int32
	keys   atomic.Value // []map[string]string
	status atomic.Int32
	cache  atomic.Value // Cache-Control header value
}

func newKeyServer(keys ...map[string]string) *keyServer {
	ks := &keyServer{}
	ks.keys.Store(keys)
	ks.status.Store(200)
	ks.cache.Store("public, max-age=3600")
	ks.Server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ks.hits.Add(1)
		if s := int(ks.status.Load()); s != 200 {
			http.Error(w, "boom", s)
			return
		}
		w.Header().Set("Cache-Control", ks.cache.Load().(string))
		_ = json.NewEncoder(w).Encode(map[string]any{"keys": ks.keys.Load()})
	}))
	return ks
}

func TestRemoteKeysAreFetchedOnceAndCached(t *testing.T) {
	k := newKey(t)
	ks := newKeyServer(jwkOf("k1", k))
	defer ks.Close()
	v := NewRemote([]string{testClient}, ks.URL)
	for i := 0; i < 3; i++ {
		if _, err := v.Verify(context.Background(), token(t, "k1", k)); err != nil {
			t.Fatal(err)
		}
	}
	if got := ks.hits.Load(); got != 1 {
		t.Errorf("fetched %d times, want 1", got)
	}
}

func TestAnUnknownKidRefetchesAtMostOncePerInterval(t *testing.T) {
	old, rotated := newKey(t), newKey(t)
	ks := newKeyServer(jwkOf("old", old))
	defer ks.Close()
	v := NewRemote([]string{testClient}, ks.URL)
	if _, err := v.Verify(context.Background(), token(t, "old", old)); err != nil {
		t.Fatal(err)
	}

	// Google rotates: the new key is published, and a token signed with it arrives right away.
	ks.keys.Store([]map[string]string{jwkOf("old", old), jwkOf("new", rotated)})
	for i := 0; i < 5; i++ {
		if _, err := v.Verify(context.Background(), token(t, "new", rotated)); !errors.Is(err, ErrInvalid) {
			t.Fatalf("within the interval an unknown kid must be rejected without refetching, got %v", err)
		}
	}
	if got := ks.hits.Load(); got != 1 {
		t.Errorf("junk tokens caused %d fetches, want 1", got)
	}

	// After the interval the next unknown kid triggers one refetch and the rotated key is accepted.
	v.mu.Lock()
	v.cache.fetchedAt = time.Now().Add(-minRefetch - time.Second)
	v.mu.Unlock()
	if _, err := v.Verify(context.Background(), token(t, "new", rotated)); err != nil {
		t.Fatalf("rotated key must be accepted after the refetch: %v", err)
	}
	if got := ks.hits.Load(); got != 2 {
		t.Errorf("fetched %d times, want 2", got)
	}
}

func TestExpiredKeysAreRefetched(t *testing.T) {
	k := newKey(t)
	ks := newKeyServer(jwkOf("k1", k))
	defer ks.Close()
	v := NewRemote([]string{testClient}, ks.URL)
	if _, err := v.Verify(context.Background(), token(t, "k1", k)); err != nil {
		t.Fatal(err)
	}
	v.mu.Lock()
	v.cache.expiresAt = time.Now().Add(-time.Second)
	v.mu.Unlock()
	if _, err := v.Verify(context.Background(), token(t, "k1", k)); err != nil {
		t.Fatal(err)
	}
	if got := ks.hits.Load(); got != 2 {
		t.Errorf("fetched %d times, want 2", got)
	}
}

func TestMaxAgeSetsTheLifetimeWithinBounds(t *testing.T) {
	k := newKey(t)
	for _, c := range []struct {
		header string
		min    time.Duration
		max    time.Duration
	}{
		{"public, max-age=7200", 7199 * time.Second, 7201 * time.Second},
		{"max-age=5", 59 * time.Second, 61 * time.Second},             // floor 60s
		{"max-age=9999999", 86399 * time.Second, 86401 * time.Second}, // cap one day
		{"no-store", 3599 * time.Second, 3601 * time.Second},          // default one hour
	} {
		ks := newKeyServer(jwkOf("k1", k))
		ks.cache.Store(c.header)
		v := NewRemote([]string{testClient}, ks.URL)
		if _, err := v.Verify(context.Background(), token(t, "k1", k)); err != nil {
			t.Fatal(err)
		}
		ttl := time.Until(v.cache.expiresAt)
		if ttl < c.min || ttl > c.max {
			t.Errorf("Cache-Control %q: lifetime %v, want about %v", c.header, ttl, c.min)
		}
		ks.Close()
	}
}

func TestAFailedFetchIsKeysUnavailableNotInvalid(t *testing.T) {
	k := newKey(t)
	ks := newKeyServer(jwkOf("k1", k))
	ks.status.Store(500)
	v := NewRemote([]string{testClient}, ks.URL)
	if _, err := v.Verify(context.Background(), token(t, "k1", k)); !errors.Is(err, ErrKeysUnavailable) {
		t.Errorf("a 500 from Google: got %v, want ErrKeysUnavailable", err)
	}
	ks.status.Store(200)
	if _, err := v.Verify(context.Background(), token(t, "k1", k)); err != nil {
		t.Errorf("must recover once Google answers again: %v", err)
	}
	ks.Close()
	if _, err := NewRemote([]string{testClient}, ks.URL).Verify(context.Background(), token(t, "k1", k)); !errors.Is(err, ErrKeysUnavailable) {
		t.Errorf("an unreachable server: got %v, want ErrKeysUnavailable", err)
	}
}

// Needs the network: LIVE_GOOGLE=1 go test ./internal/google -run Live
func TestLiveGoogleCertsParse(t *testing.T) {
	if os.Getenv("LIVE_GOOGLE") == "" {
		t.Skip("set LIVE_GOOGLE=1 to fetch Google's real signing keys")
	}
	v := NewRemote([]string{testClient}, JWKSURL)
	ks, err := v.fetch(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(ks.keys) == 0 {
		t.Fatal("no usable RSA keys parsed from Google's key set")
	}
	for kid, k := range ks.keys {
		if k.N.BitLen() < 2048 || k.E != 65537 {
			t.Errorf("key %s looks wrong: %d bits, e=%d", kid, k.N.BitLen(), k.E)
		}
	}
	t.Logf("parsed %d keys, valid for %v", len(ks.keys), time.Until(ks.expiresAt).Round(time.Second))
}
