package lib

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

// TestClientIPResolver covers the trust rules that decide which address the rate
// limiter counts. The first case is the regression test for the original bug:
// X-Forwarded-For must be ignored when no proxy is trusted.
func TestClientIPResolver(t *testing.T) {
	tests := []struct {
		name       string
		trusted    []string
		remoteAddr string
		xff        string
		want       string
	}{
		{
			name:       "no trusted proxies ignores a forged header",
			remoteAddr: "203.0.113.9:1234",
			xff:        "1.2.3.4",
			want:       "203.0.113.9",
		},
		{
			name:       "plain socket peer is stripped of its port",
			remoteAddr: "192.0.2.5:1234",
			want:       "192.0.2.5",
		},
		{
			name:       "trusted peer forwards a single hop",
			trusted:    []string{"10.0.0.0/8"},
			remoteAddr: "10.0.0.5:1234",
			xff:        "198.51.100.2",
			want:       "198.51.100.2",
		},
		{
			name:       "chain resolves to the rightmost untrusted hop",
			trusted:    []string{"10.0.0.0/8"},
			remoteAddr: "10.0.0.5:1234",
			xff:        "198.51.100.2, 10.0.0.7",
			want:       "198.51.100.2",
		},
		{
			// The attacker prepends a fake address; the proxy appends the real
			// one on the right. Only the rightmost untrusted hop is trustworthy.
			name:       "spoofed leftmost entry is ignored",
			trusted:    []string{"10.0.0.0/8"},
			remoteAddr: "10.0.0.5:1234",
			xff:        "1.2.3.4, 203.0.113.9",
			want:       "203.0.113.9",
		},
		{
			name:       "untrusted peer cannot use the header at all",
			trusted:    []string{"10.0.0.0/8"},
			remoteAddr: "203.0.113.9:1234",
			xff:        "198.51.100.2",
			want:       "203.0.113.9",
		},
		{
			name:       "an all-trusted chain falls back to the peer",
			trusted:    []string{"10.0.0.0/8"},
			remoteAddr: "10.0.0.5:1234",
			xff:        "10.0.0.6",
			want:       "10.0.0.5",
		},
		{
			name:       "a malformed hop falls back to the peer",
			trusted:    []string{"10.0.0.0/8"},
			remoteAddr: "10.0.0.5:1234",
			xff:        "not-an-ip",
			want:       "10.0.0.5",
		},
		{
			name:       "a bare configured IP trusts exactly that host",
			trusted:    []string{"192.0.2.10"},
			remoteAddr: "192.0.2.10:1234",
			xff:        "198.51.100.2",
			want:       "198.51.100.2",
		},
		{
			name:       "ipv6 peer is handled",
			remoteAddr: "[2001:db8::1]:1234",
			want:       "2001:db8::1",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Build a resolver per case so each test controls its trust list.
			resolver, err := NewClientIPResolver(tt.trusted)
			if err != nil {
				t.Fatalf("NewClientIPResolver(%v) error = %v", tt.trusted, err)
			}

			req := httptest.NewRequest(http.MethodGet, "/", nil)
			req.RemoteAddr = tt.remoteAddr
			if tt.xff != "" {
				req.Header.Set("X-Forwarded-For", tt.xff)
			}

			if got := resolver.ClientIP(req); got != tt.want {
				t.Fatalf("ClientIP = %q, want %q", got, tt.want)
			}
		})
	}
}

// TestClientIPResolverNil proves a nil resolver is safe and uses the socket peer,
// which is what main passes when no trusted proxies are configured.
func TestClientIPResolverNil(t *testing.T) {
	var resolver *ClientIPResolver

	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.RemoteAddr = "203.0.113.9:1234"
	req.Header.Set("X-Forwarded-For", "1.2.3.4")

	if got := resolver.ClientIP(req); got != "203.0.113.9" {
		t.Fatalf("ClientIP = %q, want the socket peer", got)
	}
}

// TestNewClientIPResolverRejectsBadEntries proves malformed or dangerously wide
// trust entries fail at construction instead of silently weakening the check.
func TestNewClientIPResolverRejectsBadEntries(t *testing.T) {
	tests := []struct {
		name    string
		entries []string
	}{
		{name: "invalid ip", entries: []string{"not-an-ip"}},
		{name: "invalid cidr", entries: []string{"10.0.0.0/99"}},
		{name: "trust every client", entries: []string{"0.0.0.0/0"}},
		{name: "trust every client v6", entries: []string{"::/0"}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if _, err := NewClientIPResolver(tt.entries); err == nil {
				t.Fatalf("NewClientIPResolver(%v) error = nil, want an error", tt.entries)
			}
		})
	}
}

// TestNewClientIPResolverIgnoresBlanks proves empty entries from a trailing
// comma do not break startup.
func TestNewClientIPResolverIgnoresBlanks(t *testing.T) {
	if _, err := NewClientIPResolver([]string{"", "  ", "10.0.0.0/8", ""}); err != nil {
		t.Fatalf("NewClientIPResolver() error = %v, want nil", err)
	}
}
