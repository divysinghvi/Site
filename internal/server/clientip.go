package server

import (
	"context"
	"fmt"
	"net"
	"net/http"
	"strings"
)

// ctxClientIP carries the resolved client IP (never logged, never stored in spans).
const ctxClientIP ctxKey = iota + 100

// ClientIP returns the client IP resolved by the clientIP middleware ("" when unknown).
func ClientIP(ctx context.Context) string {
	ip, _ := ctx.Value(ctxClientIP).(string)
	return ip
}

// proxyTrust decides whether X-Forwarded-For / X-Real-IP may be believed:
// only when the TCP peer is one of TRUSTED_PROXIES, or when the binary runs
// on Vercel (TrustHeaders: the platform terminates TLS and sets both).
type proxyTrust struct {
	nets         []*net.IPNet
	trustHeaders bool
}

func newProxyTrust(cidrs []string, trustHeaders bool) (*proxyTrust, error) {
	t := &proxyTrust{trustHeaders: trustHeaders}
	for _, c := range cidrs {
		c = strings.TrimSpace(c)
		if c == "" {
			continue
		}
		if !strings.Contains(c, "/") {
			if ip := net.ParseIP(c); ip != nil {
				bits := 32
				if ip.To4() == nil {
					bits = 128
				}
				c = fmt.Sprintf("%s/%d", ip.String(), bits)
			}
		}
		_, n, err := net.ParseCIDR(c)
		if err != nil {
			return nil, fmt.Errorf("TRUSTED_PROXIES: %q is not an IP or CIDR", c)
		}
		t.nets = append(t.nets, n)
	}
	return t, nil
}

func (t *proxyTrust) trusted(ip net.IP) bool {
	for _, n := range t.nets {
		if n.Contains(ip) {
			return true
		}
	}
	return false
}

// clientIP resolves the client address of r: the TCP peer, or — when that
// peer is a trusted proxy — X-Real-IP, else the right-most X-Forwarded-For
// entry that is not itself a trusted proxy.
func (t *proxyTrust) clientIP(r *http.Request) string {
	peer := hostOnly(r.RemoteAddr)
	peerIP := net.ParseIP(peer)
	trustPeer := t.trustHeaders || (peerIP != nil && t.trusted(peerIP))
	if !trustPeer {
		return peer
	}
	if v := strings.TrimSpace(r.Header.Get("X-Real-IP")); v != "" {
		if ip := net.ParseIP(hostOnly(v)); ip != nil {
			return ip.String()
		}
	}
	if xff := r.Header.Get("X-Forwarded-For"); xff != "" {
		parts := strings.Split(xff, ",")
		for i := len(parts) - 1; i >= 0; i-- {
			ip := net.ParseIP(hostOnly(strings.TrimSpace(parts[i])))
			if ip == nil {
				continue
			}
			if t.trusted(ip) {
				continue
			}
			return ip.String()
		}
	}
	return peer
}

// hostOnly strips a port (and IPv6 brackets) from host:port when present.
func hostOnly(addr string) string {
	if h, _, err := net.SplitHostPort(addr); err == nil {
		return h
	}
	return strings.Trim(addr, "[]")
}

// clientIPMiddleware stores the resolved client IP in the request context.
func (s *Server) clientIPMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ip := s.proxies.clientIP(r)
		next.ServeHTTP(w, r.WithContext(context.WithValue(r.Context(), ctxClientIP, ip)))
	})
}
