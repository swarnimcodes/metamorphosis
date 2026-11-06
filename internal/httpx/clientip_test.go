package httpx_test

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/smarniw/metamorphosis/internal/httpx"
)

func TestClientIPBelievesProxyHeadersOnlyFromTheLocalMachine(t *testing.T) {
	for _, c := range []struct {
		name    string
		remote  string
		headers map[string]string
		want    string
	}{
		{"Cloudflare's header behind the local proxy", "127.0.0.1:5000", map[string]string{"CF-Connecting-IP": "203.0.113.7"}, "203.0.113.7"},
		{"IPv6 loopback proxy", "[::1]:5000", map[string]string{"CF-Connecting-IP": "2001:db8::1"}, "2001:db8::1"},
		{"the proxy's own forwarding header when there is no Cloudflare", "127.0.0.1:5000", map[string]string{"X-Forwarded-For": "198.51.100.9, 10.0.0.1"}, "198.51.100.9"},
		{"Cloudflare's header wins over forwarding", "127.0.0.1:5000", map[string]string{"CF-Connecting-IP": "203.0.113.7", "X-Forwarded-For": "198.51.100.9"}, "203.0.113.7"},
		{"a stranger cannot name their own address", "198.51.100.20:5000", map[string]string{"CF-Connecting-IP": "203.0.113.7", "X-Forwarded-For": "203.0.113.8"}, "198.51.100.20"},
		{"a garbage header is ignored", "127.0.0.1:5000", map[string]string{"CF-Connecting-IP": "not-an-ip"}, "127.0.0.1"},
		{"no headers", "127.0.0.1:5000", nil, "127.0.0.1"},
	} {
		r := httptest.NewRequest(http.MethodGet, "/", nil)
		r.RemoteAddr = c.remote
		for k, v := range c.headers {
			r.Header.Set(k, v)
		}
		if got := httpx.ClientIP(r); got != c.want {
			t.Errorf("%s: got %q, want %q", c.name, got, c.want)
		}
	}
}
