package service

import (
	"context"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"time"
)

// CheckOutboundURL validates an administrator-supplied URL before the panel
// makes a request to it. Without this, features such as "remote panel" or the
// external traffic inform URI let the panel (running as root) be pointed at
// loopback, RFC1918 or cloud metadata addresses, turning them into an SSRF
// primitive.
func CheckOutboundURL(ctx context.Context, rawURL string) error {
	u, err := url.Parse(strings.TrimSpace(rawURL))
	if err != nil {
		return fmt.Errorf("invalid url: %w", err)
	}
	if u.Scheme != "http" && u.Scheme != "https" {
		return fmt.Errorf("unsupported url scheme: %q", u.Scheme)
	}
	host := u.Hostname()
	if host == "" {
		return fmt.Errorf("url has no host")
	}
	return checkSSRF(ctx, host)
}

// newSafeHTTPClient returns a client whose dialer re-validates every resolved
// address, so a hostname that passes CheckOutboundURL and then resolves to an
// internal address (DNS rebinding) is still refused.
func newSafeHTTPClient(timeout time.Duration) *http.Client {
	return &http.Client{
		Timeout:   timeout,
		Transport: ssrfSafeTransport(),
	}
}
