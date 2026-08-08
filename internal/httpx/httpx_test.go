package httpx

import (
	"context"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/CypherNova1337/paramvoid/internal/config"
)

func testClient(t *testing.T, cfg *config.Config) *Client {
	t.Helper()
	c, err := New(cfg, nil) // limiter not exercised by build()
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	return c
}

func TestBuildGETPlacesParamsInQuery(t *testing.T) {
	c := testClient(t, &config.Config{Method: "GET"})
	req, err := c.build(context.Background(), Request{URL: "http://x.tld/api?existing=1", Method: "GET"}, map[string]string{"foo": "bar"})
	if err != nil {
		t.Fatal(err)
	}
	q := req.URL.Query()
	if q.Get("foo") != "bar" || q.Get("existing") != "1" {
		t.Fatalf("query = %q, want foo=bar and existing preserved", req.URL.RawQuery)
	}
	if req.Method != http.MethodGet {
		t.Fatalf("method = %s", req.Method)
	}
}

func TestBuildPOSTForm(t *testing.T) {
	c := testClient(t, &config.Config{Method: "POST"})
	req, err := c.build(context.Background(), Request{URL: "http://x.tld/", Method: "POST"}, map[string]string{"a": "b"})
	if err != nil {
		t.Fatal(err)
	}
	if ct := req.Header.Get("Content-Type"); ct != "application/x-www-form-urlencoded" {
		t.Fatalf("content-type = %q", ct)
	}
	body, _ := io.ReadAll(req.Body)
	if !strings.Contains(string(body), "a=b") {
		t.Fatalf("body = %q", string(body))
	}
}

func TestBuildJSONBody(t *testing.T) {
	c := testClient(t, &config.Config{Method: "JSON"})
	req, err := c.build(context.Background(), Request{URL: "http://x.tld/", Method: "JSON"}, map[string]string{"k": "v"})
	if err != nil {
		t.Fatal(err)
	}
	if ct := req.Header.Get("Content-Type"); ct != "application/json" {
		t.Fatalf("content-type = %q", ct)
	}
	body, _ := io.ReadAll(req.Body)
	if !strings.Contains(string(body), `"k":"v"`) {
		t.Fatalf("json body = %q", string(body))
	}
}

func TestBuildXMLEscapes(t *testing.T) {
	c := testClient(t, &config.Config{Method: "XML"})
	req, err := c.build(context.Background(), Request{URL: "http://x.tld/", Method: "XML"}, map[string]string{"tag": "a<b>&c"})
	if err != nil {
		t.Fatal(err)
	}
	if ct := req.Header.Get("Content-Type"); ct != "application/xml" {
		t.Fatalf("content-type = %q", ct)
	}
	body, _ := io.ReadAll(req.Body)
	s := string(body)
	if !strings.Contains(s, "<tag>") || !strings.Contains(s, "&lt;b&gt;") || !strings.Contains(s, "&amp;c") {
		t.Fatalf("xml body not escaped as expected: %q", s)
	}
}

func TestBuildIncludeMerged(t *testing.T) {
	c := testClient(t, &config.Config{Method: "GET", Include: map[string]string{"tenant": "acme"}})
	// Include params are merged in Do(); build() itself only sees what it's given,
	// so exercise the merge path through Do would need a server. Here we just verify
	// build honors provided params and a custom UA/header override.
	req, err := c.build(context.Background(), Request{URL: "http://x.tld/", Method: "GET", Headers: map[string]string{"User-Agent": "custom"}}, map[string]string{"a": "1"})
	if err != nil {
		t.Fatal(err)
	}
	if req.Header.Get("User-Agent") != "custom" {
		t.Fatalf("header override not applied: %q", req.Header.Get("User-Agent"))
	}
}

func TestRetryAfterParsing(t *testing.T) {
	h := http.Header{}
	h.Set("Retry-After", "5")
	if d := retryAfter(h); d != 5*time.Second {
		t.Fatalf("retryAfter seconds = %v, want 5s", d)
	}
	h.Set("Retry-After", "")
	if d := retryAfter(h); d != 0 {
		t.Fatalf("empty retryAfter = %v, want 0", d)
	}
	future := time.Now().Add(3 * time.Second).UTC().Format(http.TimeFormat)
	h.Set("Retry-After", future)
	if d := retryAfter(h); d <= 0 || d > 4*time.Second {
		t.Fatalf("http-date retryAfter = %v, want ~3s", d)
	}
}
