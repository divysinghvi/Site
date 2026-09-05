package collector

import (
	"net/http"
	"strings"
	"time"

	"divy.dev/internal/version"
)

// UserAgent is the outbound identity of the API's collectors:
// "divy.dev-collector/<version> (+<site origin>)".
func UserAgent(siteOrigin string) string {
	return "divy.dev-collector/" + version.Version + " (+" + strings.TrimRight(siteOrigin, "/") + ")"
}

// NewHTTPClient is the shared outbound client of the GitHub and PyPI
// collectors: a hard timeout, no cookies, no proxy surprises beyond the
// environment's, keep-alives on (one host per collector).
func NewHTTPClient(timeout time.Duration) *http.Client {
	if timeout <= 0 {
		timeout = 30 * time.Second
	}
	return &http.Client{
		Timeout: timeout,
		Transport: &http.Transport{
			Proxy:                 http.ProxyFromEnvironment,
			ForceAttemptHTTP2:     true,
			MaxIdleConnsPerHost:   4,
			IdleConnTimeout:       60 * time.Second,
			TLSHandshakeTimeout:   10 * time.Second,
			ResponseHeaderTimeout: timeout,
		},
	}
}
