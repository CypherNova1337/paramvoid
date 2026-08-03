// Package httpx wraps net/http with the behaviour Arjun-go needs: per-method
// parameter placement, proxy/redirect control, response normalization, and an
// adaptive retry loop that cooperates with the rate limiter so that rate
// limiting and transient errors are absorbed rather than fatal.
package httpx

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"sync/atomic"
	"time"

	"github.com/CypherNova1337/paramvoid/internal/config"
	"github.com/CypherNova1337/paramvoid/internal/ratelimit"
)

// Request describes a target endpoint independent of the params being tested.
type Request struct {
	URL     string
	Method  string
	Headers map[string]string
}

// Response is a normalized view of an HTTP response with the derived metrics the
// anomaly detector compares against. The body is fully read and stored.
type Response struct {
	StatusCode int
	Body       string
	Header     http.Header
	Location   string // redirect target (Location header), "" if none
	Length     int    // len(body)
	Words      int    // whitespace-separated token count
	Lines      int    // newline count

	// RateLimited is true when this response is a rate-limit status returned only
	// because per-request retries were exhausted. The scanner treats such
	// responses as inconclusive (never as a discovered parameter).
	RateLimited bool
}

// Client is a configured HTTP client with an attached adaptive limiter.
type Client struct {
	hc      *http.Client
	cfg     *config.Config
	limiter *ratelimit.Limiter

	sent  int64 // total requests actually sent (atomic)
	codes map[int]bool
}

// New builds a Client. Redirect and proxy behaviour come from cfg.
func New(cfg *config.Config, limiter *ratelimit.Limiter) (*Client, error) {
	transport := &http.Transport{
		MaxIdleConns:        100,
		MaxIdleConnsPerHost: 100,
		IdleConnTimeout:     30 * time.Second,
	}

	// Route through Burp (or any proxy) so discovered params are visible there.
	if cfg.BurpProxy != "" {
		proxyURL := cfg.BurpProxy
		if !strings.Contains(proxyURL, "://") {
			proxyURL = "http://" + proxyURL
		}
		pu, err := url.Parse(proxyURL)
		if err != nil {
			return nil, fmt.Errorf("invalid proxy %q: %w", cfg.BurpProxy, err)
		}
		transport.Proxy = http.ProxyURL(pu)
	}

	hc := &http.Client{
		Transport: transport,
		Timeout:   cfg.Timeout,
	}
	if cfg.DisableRedirect {
		hc.CheckRedirect = func(*http.Request, []*http.Request) error {
			return http.ErrUseLastResponse
		}
	}

	codes := map[int]bool{}
	for _, c := range cfg.RateCodes {
		codes[c] = true
	}
	if len(codes) == 0 {
		codes[429] = true
		codes[503] = true
	}

	return &Client{hc: hc, cfg: cfg, limiter: limiter, codes: codes}, nil
}

// Sent returns the total number of requests dispatched.
func (c *Client) Sent() int64 { return atomic.LoadInt64(&c.sent) }

// Do sends req with the given test params applied per method, transparently
// retrying through rate limiting and transient errors. It only returns an error
// when retries are exhausted or the context is cancelled.
func (c *Client) Do(ctx context.Context, req Request, params map[string]string) (*Response, error) {
	// Merge in the always-included params.
	merged := params
	if len(c.cfg.Include) > 0 {
		merged = make(map[string]string, len(params)+len(c.cfg.Include))
		for k, v := range c.cfg.Include {
			merged[k] = v
		}
		for k, v := range params {
			merged[k] = v
		}
	}

	const maxRateRetries = 12 // per-request cap so a permanently-blocked host still terminates

	for attempt := 0; ; attempt++ {
		if err := c.limiter.Wait(ctx); err != nil {
			return nil, err
		}

		httpReq, err := c.build(ctx, req, merged)
		if err != nil {
			return nil, err // build errors are not retryable
		}

		atomic.AddInt64(&c.sent, 1)
		resp, err := c.hc.Do(httpReq)
		if err != nil {
			c.limiter.PenalizeNetwork()
			if attempt < c.cfg.MaxRetries {
				continue
			}
			return nil, err
		}

		out, nerr := normalize(resp)
		if nerr != nil {
			c.limiter.PenalizeNetwork()
			if attempt < c.cfg.MaxRetries {
				continue
			}
			return nil, nerr
		}

		if c.codes[out.StatusCode] {
			c.limiter.Penalize(retryAfter(out.Header))
			if attempt < maxRateRetries {
				continue // resend the SAME params after cooldown; nothing is lost
			}
			// Give up retrying but hand the response back, tagged inconclusive.
			out.RateLimited = true
			return out, nil
		}

		c.limiter.Reward()
		return out, nil
	}
}

// build constructs the *http.Request, placing params according to the method.
func (c *Client) build(ctx context.Context, req Request, params map[string]string) (*http.Request, error) {
	method := strings.ToUpper(req.Method)

	var (
		httpReq *http.Request
		err     error
	)

	switch method {
	case "GET", "": // GET (default): params in query string
		u, perr := url.Parse(req.URL)
		if perr != nil {
			return nil, perr
		}
		q := u.Query()
		for k, v := range params {
			q.Set(k, v)
		}
		u.RawQuery = q.Encode()
		httpReq, err = http.NewRequestWithContext(ctx, http.MethodGet, u.String(), nil)

	case "POST": // form-encoded body
		form := url.Values{}
		for k, v := range params {
			form.Set(k, v)
		}
		httpReq, err = http.NewRequestWithContext(ctx, http.MethodPost, req.URL, strings.NewReader(form.Encode()))
		if err == nil {
			httpReq.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		}

	case "JSON": // JSON body
		body, merr := json.Marshal(params)
		if merr != nil {
			return nil, merr
		}
		httpReq, err = http.NewRequestWithContext(ctx, http.MethodPost, req.URL, bytes.NewReader(body))
		if err == nil {
			httpReq.Header.Set("Content-Type", "application/json")
		}

	case "XML": // simple <root><key>value</key>...</root> body
		var b strings.Builder
		b.WriteString("<root>")
		for k, v := range params {
			b.WriteString("<" + k + ">" + xmlEscape(v) + "</" + k + ">")
		}
		b.WriteString("</root>")
		httpReq, err = http.NewRequestWithContext(ctx, http.MethodPost, req.URL, strings.NewReader(b.String()))
		if err == nil {
			httpReq.Header.Set("Content-Type", "application/xml")
		}

	default:
		return nil, fmt.Errorf("unsupported method %q (use GET/POST/JSON/XML)", req.Method)
	}

	if err != nil {
		return nil, err
	}

	// A reasonable default UA; overridable via --headers.
	httpReq.Header.Set("User-Agent", "paramvoid/1.0")
	for k, v := range req.Headers {
		httpReq.Header.Set(k, v)
	}
	return httpReq, nil
}

// normalize reads the body and computes the derived metrics.
func normalize(resp *http.Response) (*Response, error) {
	defer resp.Body.Close()
	// Guard against pathological bodies; 12 MiB is plenty for param discovery.
	limited := io.LimitReader(resp.Body, 12<<20)
	raw, err := io.ReadAll(limited)
	if err != nil {
		return nil, err
	}
	body := string(raw)
	return &Response{
		StatusCode: resp.StatusCode,
		Body:       body,
		Header:     resp.Header,
		Location:   resp.Header.Get("Location"),
		Length:     len(body),
		Words:      len(strings.Fields(body)),
		Lines:      strings.Count(body, "\n"),
	}, nil
}

// retryAfter parses a Retry-After header (seconds or HTTP-date) into a duration.
func retryAfter(h http.Header) time.Duration {
	v := strings.TrimSpace(h.Get("Retry-After"))
	if v == "" {
		return 0
	}
	if secs, err := strconv.Atoi(v); err == nil {
		return time.Duration(secs) * time.Second
	}
	if t, err := http.ParseTime(v); err == nil {
		if d := time.Until(t); d > 0 {
			return d
		}
	}
	return 0
}

func xmlEscape(s string) string {
	r := strings.NewReplacer("&", "&amp;", "<", "&lt;", ">", "&gt;")
	return r.Replace(s)
}
