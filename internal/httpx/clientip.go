package httpx

import (
	"net"
	"net/http"
	"net/netip"
	"strings"
)

// ClientIP is the address of the person making the request. In production the API listens only on loopback behind
// Caddy and Cloudflare, so every connection comes from 127.0.0.1 and the real address is in the header Cloudflare
// sets. That header is believed only when the connection itself is from the local machine: anyone who could
// reach the API directly could otherwise name any address they like.
func ClientIP(r *http.Request) string {
	remote := r.RemoteAddr
	if host, _, err := net.SplitHostPort(remote); err == nil {
		remote = host
	}
	addr, err := netip.ParseAddr(remote)
	if err != nil || !addr.IsLoopback() {
		return remote
	}
	if v := strings.TrimSpace(r.Header.Get("CF-Connecting-IP")); v != "" {
		if ip, err := netip.ParseAddr(v); err == nil {
			return ip.String()
		}
	}
	// Without Cloudflare (a direct call through Caddy, local testing) the proxy's own header is the next best.
	if v, _, _ := strings.Cut(r.Header.Get("X-Forwarded-For"), ","); strings.TrimSpace(v) != "" {
		if ip, err := netip.ParseAddr(strings.TrimSpace(v)); err == nil {
			return ip.String()
		}
	}
	return remote
}
