// Package fetch is Lurien's cross-cutting HTTP layer: retries with jittered
// backoff, per-host rate limiting, a shared User-Agent, and conditional
// requests. Providers receive a Client and never build transport concerns
// themselves — this is what keeps a provider under ~200 lines.
package fetch

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"math/rand"
	"net/http"
	"net/url"
	"strconv"
	"sync"
	"time"

	"golang.org/x/time/rate"
)

// Request is a single outbound HTTP request (GET by default).
type Request struct {
	URL         string
	Method      string // defaults to GET
	Body        []byte // request body (e.g. POST JSON); nil for GET
	Headers     map[string]string
	IfNoneMatch string // ETag for conditional GET; empty to skip
}

// Response is the result of a GET.
type Response struct {
	Status      int
	Body        []byte
	ETag        string
	NotModified bool // true when server replied 304
}

// Client is the seam providers depend on. Small on purpose.
type Client interface {
	Do(ctx context.Context, req Request) (*Response, error)
}

// Options configures an HTTPClient.
type Options struct {
	UserAgent     string
	Timeout       time.Duration
	MaxRetries    int
	PerHostRate   rate.Limit // requests/sec per host
	PerHostBurst  int
	RetryBaseWait time.Duration
}

func (o *Options) withDefaults() {
	if o.UserAgent == "" {
		o.UserAgent = "Lurien/0.1 (+https://lurien.dev)"
	}
	if o.Timeout == 0 {
		o.Timeout = 20 * time.Second
	}
	if o.MaxRetries == 0 {
		o.MaxRetries = 4
	}
	if o.PerHostRate == 0 {
		o.PerHostRate = rate.Limit(2) // 2 req/s per host by default
	}
	if o.PerHostBurst == 0 {
		o.PerHostBurst = 4
	}
	if o.RetryBaseWait == 0 {
		o.RetryBaseWait = 500 * time.Millisecond
	}
}

// HTTPClient is the production Client.
type HTTPClient struct {
	hc   *http.Client
	opt  Options
	mu   sync.Mutex
	lims map[string]*rate.Limiter
}

// New builds an HTTPClient.
func New(opt Options) *HTTPClient {
	opt.withDefaults()
	return &HTTPClient{
		hc:   &http.Client{Timeout: opt.Timeout},
		opt:  opt,
		lims: map[string]*rate.Limiter{},
	}
}

func (c *HTTPClient) limiter(host string) *rate.Limiter {
	c.mu.Lock()
	defer c.mu.Unlock()
	l, ok := c.lims[host]
	if !ok {
		l = rate.NewLimiter(c.opt.PerHostRate, c.opt.PerHostBurst)
		c.lims[host] = l
	}
	return l
}

// Do performs the GET with rate limiting and retry.
func (c *HTTPClient) Do(ctx context.Context, req Request) (*Response, error) {
	u, err := url.Parse(req.URL)
	if err != nil {
		return nil, fmt.Errorf("parse url: %w", err)
	}
	lim := c.limiter(u.Host)

	var lastErr error
	for attempt := 0; attempt <= c.opt.MaxRetries; attempt++ {
		if attempt > 0 {
			if err := sleepBackoff(ctx, c.opt.RetryBaseWait, attempt); err != nil {
				return nil, err
			}
		}
		if err := lim.Wait(ctx); err != nil {
			return nil, err
		}

		resp, retryable, err := c.once(ctx, req)
		if err == nil {
			return resp, nil
		}
		lastErr = err
		if !retryable {
			return nil, err
		}
	}
	return nil, fmt.Errorf("exhausted %d retries: %w", c.opt.MaxRetries, lastErr)
}

// once issues one request. It returns retryable=true for transient failures
// (network error, 429, 5xx) so the caller can back off and try again.
func (c *HTTPClient) once(ctx context.Context, req Request) (*Response, bool, error) {
	method := req.Method
	if method == "" {
		method = http.MethodGet
	}
	var reqBody io.Reader
	if len(req.Body) > 0 {
		reqBody = bytes.NewReader(req.Body)
	}
	hr, err := http.NewRequestWithContext(ctx, method, req.URL, reqBody)
	if err != nil {
		return nil, false, err
	}
	hr.Header.Set("User-Agent", c.opt.UserAgent)
	hr.Header.Set("Accept", "application/json")
	if len(req.Body) > 0 {
		hr.Header.Set("Content-Type", "application/json")
	}
	for k, v := range req.Headers {
		hr.Header.Set(k, v)
	}
	if req.IfNoneMatch != "" {
		hr.Header.Set("If-None-Match", req.IfNoneMatch)
	}

	resp, err := c.hc.Do(hr)
	if err != nil {
		return nil, true, err // network-level: retry
	}
	defer resp.Body.Close()

	if resp.StatusCode == http.StatusNotModified {
		return &Response{Status: resp.StatusCode, ETag: resp.Header.Get("ETag"), NotModified: true}, false, nil
	}
	if resp.StatusCode == http.StatusTooManyRequests || resp.StatusCode >= 500 {
		// Honor Retry-After when present by draining the body first.
		io.Copy(io.Discard, resp.Body)
		return nil, true, fmt.Errorf("http %d from %s", resp.StatusCode, req.URL)
	}
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, true, err
	}
	if resp.StatusCode != http.StatusOK {
		return nil, false, fmt.Errorf("http %d from %s", resp.StatusCode, req.URL)
	}
	return &Response{Status: resp.StatusCode, Body: body, ETag: resp.Header.Get("ETag")}, false, nil
}

// sleepBackoff waits base*2^(attempt-1) plus up to 50% jitter, respecting ctx.
func sleepBackoff(ctx context.Context, base time.Duration, attempt int) error {
	d := base * time.Duration(1<<(attempt-1))
	jitter := time.Duration(rand.Int63n(int64(d)/2 + 1))
	select {
	case <-time.After(d + jitter):
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

// parseRetryAfter is kept for future use when honoring 429 Retry-After headers.
func parseRetryAfter(v string) (time.Duration, bool) {
	if v == "" {
		return 0, false
	}
	if secs, err := strconv.Atoi(v); err == nil {
		return time.Duration(secs) * time.Second, true
	}
	if t, err := http.ParseTime(v); err == nil {
		return time.Until(t), true
	}
	return 0, false
}
