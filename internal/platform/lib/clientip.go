package lib

import (
	"fmt"
	"net"
	"net/http"
	"strings"
)

// ClientIPResolver derives the originating client IP for a request.
//
// X-Forwarded-For is client-controlled: any caller can send it. It is therefore
// honoured only when the socket peer is one of the configured trusted proxies,
// so a direct caller can never pick its own rate-limit bucket.
type ClientIPResolver struct {
	// trusted holds the networks whose X-Forwarded-For header is believed.
	trusted []*net.IPNet
}

// NewClientIPResolver parses configured trusted-proxy entries. Each entry is a
// CIDR ("10.0.0.0/8") or a bare IP ("192.0.2.10"), which becomes a single-host
// network. An invalid entry is an error so a typo fails at boot instead of
// silently disabling or widening the trust decision.
func NewClientIPResolver(entries []string) (*ClientIPResolver, error) {
	// Pre-size the slice: one network per configured entry, ignoring blanks.
	resolver := &ClientIPResolver{trusted: make([]*net.IPNet, 0, len(entries))}

	for _, raw := range entries {
		entry := strings.TrimSpace(raw)
		if entry == "" {
			continue
		}

		// A bare address becomes a host route so both spellings are accepted.
		if !strings.Contains(entry, "/") {
			ip := net.ParseIP(entry)
			if ip == nil {
				return nil, fmt.Errorf("clientip: trusted proxy %q is not an IP or CIDR", raw)
			}

			// Use the 32-bit form for IPv4 so the mask covers the right width.
			bits := 8 * net.IPv6len
			if v4 := ip.To4(); v4 != nil {
				ip = v4
				bits = 8 * net.IPv4len
			}
			resolver.trusted = append(resolver.trusted, &net.IPNet{
				IP:   ip,
				Mask: net.CIDRMask(bits, bits),
			})
			continue
		}

		// A CIDR must parse before it can be trusted.
		_, network, err := net.ParseCIDR(entry)
		if err != nil {
			return nil, fmt.Errorf("clientip: trusted proxy %q is not a valid CIDR: %w", raw, err)
		}

		// A zero-length prefix would trust every address on the internet, which
		// re-opens exactly the spoofing hole this resolver exists to close.
		if ones, _ := network.Mask.Size(); ones == 0 {
			return nil, fmt.Errorf("clientip: trusted proxy %q would trust every client", raw)
		}
		resolver.trusted = append(resolver.trusted, network)
	}

	return resolver, nil
}

// ClientIP returns the originating client address: the rightmost
// X-Forwarded-For hop that is not itself a trusted proxy, or the socket peer.
// A nil resolver is safe and always returns the socket peer, which is the
// correct default for a directly exposed API.
func (r *ClientIPResolver) ClientIP(req *http.Request) string {
	// Start from the only address the client cannot forge: the TCP peer.
	peer := remoteHost(req.RemoteAddr)

	// With no trusted proxies configured, the spoofable header is ignored
	// entirely and the peer is authoritative.
	if r == nil || len(r.trusted) == 0 {
		return peer
	}

	// The header is only meaningful when the request actually came through a
	// proxy we operate. A direct caller lands here and gets its real address.
	if !r.isTrusted(net.ParseIP(peer)) {
		return peer
	}

	// Walk right to left: the rightmost address a proxy appended is closest to
	// us, while anything a client injected sits further to the left.
	hops := strings.Split(req.Header.Get("X-Forwarded-For"), ",")
	for i := len(hops) - 1; i >= 0; i-- {
		hop := strings.TrimSpace(hops[i])
		if hop == "" {
			continue
		}

		ip := net.ParseIP(hop)
		// A malformed hop means the chain cannot be trusted, so stop and use
		// the peer rather than guessing.
		if ip == nil {
			return peer
		}

		// Skip our own proxies; the first other address is the real client.
		if !r.isTrusted(ip) {
			return hop
		}
	}

	// Every hop was a trusted proxy (or there were none), so the peer is the
	// most specific address available.
	return peer
}

// isTrusted reports whether ip falls inside any configured proxy network.
func (r *ClientIPResolver) isTrusted(ip net.IP) bool {
	// net.IPNet.Contains is false for nil, but bail out early to make that obvious.
	if ip == nil {
		return false
	}
	for _, network := range r.trusted {
		if network.Contains(ip) {
			return true
		}
	}
	return false
}

// remoteHost strips the port from RemoteAddr, tolerating a bare host.
func remoteHost(remoteAddr string) string {
	host, _, err := net.SplitHostPort(remoteAddr)
	if err != nil {
		// No port present, so the value is already a host.
		return remoteAddr
	}
	return host
}
