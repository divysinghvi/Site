// Package github collects Divy's GitHub activity through the GraphQL API:
// commits and calendar contributions per day (365-day backfill), merged pull
// requests by owner and public repository (full history, resumable scan),
// stars, followers and open OSS pull requests. It needs DIVY_GITHUB_TOKEN;
// without it every run is reported as skipped and nothing is faked.
package github

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"
)

// DefaultEndpoint is GitHub's GraphQL endpoint.
const DefaultEndpoint = "https://api.github.com/graphql"

// Sentinel errors of the transport.
var (
	ErrTokenRejected = errors.New("github: token rejected (401)")
	ErrRateLimited   = errors.New("github: rate limited")
)

// rateLimit is the rateLimit object every query selects.
type rateLimit struct {
	Cost      int    `json:"cost"`
	Remaining int    `json:"remaining"`
	ResetAt   string `json:"resetAt"`
}

type gqlError struct {
	Message string `json:"message"`
	Type    string `json:"type"`
}

type gqlResponse struct {
	Data   json.RawMessage `json:"data"`
	Errors []gqlError      `json:"errors"`
}

// client is the GraphQL transport: bearer auth, one retry on network errors
// and 5xx, rate-limit detection, token redaction in every error.
type client struct {
	endpoint   string
	token      string
	userAgent  string
	http       *http.Client
	retryDelay time.Duration
}

func (c *client) redact(s string) string {
	if c.token == "" {
		return s
	}
	return strings.ReplaceAll(s, c.token, "[redacted]")
}

// query posts one GraphQL document and decodes data into out. The returned
// rateLimit is taken from data.rateLimit (zero when the query did not select it).
func (c *client) query(ctx context.Context, doc string, vars map[string]any, out any) (rateLimit, error) {
	body, err := json.Marshal(map[string]any{"query": doc, "variables": vars})
	if err != nil {
		return rateLimit{}, err
	}
	var lastErr error
	for attempt := 0; attempt < 2; attempt++ {
		if attempt > 0 {
			t := time.NewTimer(c.retryDelay)
			select {
			case <-ctx.Done():
				t.Stop()
				return rateLimit{}, ctx.Err()
			case <-t.C:
			}
		}
		rl, retry, err := c.once(ctx, body, out)
		if err == nil {
			return rl, nil
		}
		lastErr = err
		if !retry || ctx.Err() != nil {
			break
		}
	}
	return rateLimit{}, lastErr
}

func (c *client) once(ctx context.Context, body []byte, out any) (rl rateLimit, retry bool, err error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.endpoint, bytes.NewReader(body))
	if err != nil {
		return rl, false, err
	}
	req.Header.Set("Authorization", "bearer "+c.token)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json")
	req.Header.Set("User-Agent", c.userAgent)
	resp, err := c.http.Do(req)
	if err != nil {
		if ctx.Err() != nil {
			// Keep the context error in the chain: fetchContributions cancels
			// its sibling windows after a real failure and must recognise the
			// cancellations it caused (errors.Is) to report the real error.
			return rl, false, fmt.Errorf("github: request: %w", ctx.Err())
		}
		return rl, true, fmt.Errorf("github: request: %s", c.redact(err.Error()))
	}
	defer func() { _ = resp.Body.Close() }()
	raw, err := io.ReadAll(io.LimitReader(resp.Body, 8<<20))
	if err != nil {
		return rl, true, fmt.Errorf("github: read response: %w", err)
	}
	switch {
	case resp.StatusCode == http.StatusUnauthorized:
		return rl, false, ErrTokenRejected
	case resp.StatusCode == http.StatusForbidden || resp.StatusCode == http.StatusTooManyRequests:
		return rl, false, fmt.Errorf("%w until %s (HTTP %d)", ErrRateLimited, resetFromHeaders(resp.Header), resp.StatusCode)
	case resp.StatusCode >= 500:
		return rl, true, fmt.Errorf("github: HTTP %d", resp.StatusCode)
	case resp.StatusCode != http.StatusOK:
		return rl, false, fmt.Errorf("github: HTTP %d: %s", resp.StatusCode, c.redact(truncate(string(raw), 200)))
	}
	if resp.Header.Get("x-ratelimit-remaining") == "0" {
		return rl, false, fmt.Errorf("%w until %s (x-ratelimit-remaining: 0)", ErrRateLimited, resetFromHeaders(resp.Header))
	}
	var env gqlResponse
	if err := json.Unmarshal(raw, &env); err != nil {
		return rl, false, fmt.Errorf("github: decode: %w", err)
	}
	if len(env.Errors) > 0 {
		e := env.Errors[0]
		if strings.EqualFold(e.Type, "RATE_LIMITED") {
			return rl, false, fmt.Errorf("%w: %s", ErrRateLimited, c.redact(e.Message))
		}
		return rl, false, fmt.Errorf("github: graphql: %s", c.redact(e.Message))
	}
	if len(env.Data) == 0 || string(env.Data) == "null" {
		return rl, false, errors.New("github: empty data in response")
	}
	var rlOnly struct {
		RateLimit rateLimit `json:"rateLimit"`
	}
	_ = json.Unmarshal(env.Data, &rlOnly)
	if out != nil {
		if err := json.Unmarshal(env.Data, out); err != nil {
			return rl, false, fmt.Errorf("github: decode data: %w", err)
		}
	}
	return rlOnly.RateLimit, false, nil
}

func resetFromHeaders(h http.Header) string {
	if v := h.Get("x-ratelimit-reset"); v != "" {
		if n, err := strconv.ParseInt(v, 10, 64); err == nil {
			return time.Unix(n, 0).UTC().Format(time.RFC3339)
		}
	}
	if v := h.Get("retry-after"); v != "" {
		return "retry-after " + v + "s"
	}
	return "unknown"
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "…"
}
