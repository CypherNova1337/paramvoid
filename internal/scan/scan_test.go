package scan

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"sort"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/CypherNova1337/paramvoid/internal/config"
	"github.com/CypherNova1337/paramvoid/internal/httpx"
	"github.com/CypherNova1337/paramvoid/internal/ratelimit"
)

// paramNames extracts the parameter names the handler received, across GET query,
// form body, and JSON body, so a single test handler works for every method.
func paramNames(r *http.Request) map[string]bool {
	names := map[string]bool{}
	_ = r.ParseForm()
	for k := range r.Form {
		names[k] = true
	}
	if strings.Contains(r.Header.Get("Content-Type"), "application/json") {
		body, _ := io.ReadAll(io.LimitReader(r.Body, 1<<20))
		var m map[string]interface{}
		if json.Unmarshal(body, &m) == nil {
			for k := range m {
				names[k] = true
			}
		}
	}
	return names
}

// runScan wires up a client + config and scans one URL, returning the confirmed
// params (sorted), the status, and the total number of HTTP requests sent.
func runScan(t *testing.T, method, url string, words []string) ([]string, Status, int64) {
	t.Helper()
	cfg := &config.Config{
		Method:     method,
		Timeout:    10 * time.Second,
		Workers:    4,
		ChunkSize:  50,
		RateLimit:  500,
		RateMin:    1,
		RateAdapt:  true,
		MaxRetries: 2,
	}
	lim := ratelimit.New(cfg.RateLimit, cfg.RateMin, cfg.RateAdapt)
	client, err := httpx.New(cfg, lim)
	if err != nil {
		t.Fatalf("httpx.New: %v", err)
	}
	req := httpx.Request{URL: url, Method: method}
	res, status := Run(context.Background(), client, cfg, req, words, nil)
	got := append([]string(nil), res.Params...)
	sort.Strings(got)
	return got, status, client.Sent()
}

// baseWords is a wordlist that contains the "real" params plus lots of noise.
func baseWords(real ...string) []string {
	words := append([]string(nil), real...)
	for i := 0; i < 300; i++ {
		words = append(words, fmt.Sprintf("noise_param_%d", i))
	}
	return words
}

func eq(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

// A server that reacts (changes body length) to a fixed set of real params.
func TestScanSimpleGET(t *testing.T) {
	real := map[string]bool{"user_id": true, "debug": true, "redirect": true}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		got := paramNames(r)
		extra := ""
		for k := range real {
			if got[k] {
				extra += "\nknown:" + k
			}
		}
		fmt.Fprintf(w, "<html><body>hello%s</body></html>", extra)
	}))
	defer srv.Close()

	params, status, _ := runScan(t, "GET", srv.URL, baseWords("user_id", "debug", "redirect"))
	if status != StatusOK {
		t.Fatalf("status = %v, want OK", status)
	}
	if want := []string{"debug", "redirect", "user_id"}; !eq(params, want) {
		t.Fatalf("params = %v, want %v", params, want)
	}
}

func TestScanPOSTandJSON(t *testing.T) {
	for _, method := range []string{"POST", "JSON"} {
		t.Run(method, func(t *testing.T) {
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				got := paramNames(r)
				extra := ""
				if got["api_key"] {
					extra = " known"
				}
				fmt.Fprintf(w, "<html><body>ok%s</body></html>", extra)
			}))
			defer srv.Close()
			params, status, _ := runScan(t, method, srv.URL, baseWords("api_key"))
			if status != StatusOK || !eq(params, []string{"api_key"}) {
				t.Fatalf("%s: params=%v status=%v, want [api_key] OK", method, params, status)
			}
		})
	}
}

// Regression test for the reflective-page expansion bug: a page that echoes every
// parameter it receives must be handled cheaply (no full-wordlist expansion) and
// must yield zero false positives.
func TestScanReflectiveIsCheapAndClean(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = r.ParseForm()
		var b strings.Builder
		b.WriteString("<html><body>")
		for k, v := range r.Form {
			fmt.Fprintf(&b, "<div>%s=%s</div>", k, v[0])
		}
		b.WriteString("</body></html>")
		_, _ = w.Write([]byte(b.String()))
	}))
	defer srv.Close()

	words := baseWords() // 300 noise words, no real params
	params, _, sent := runScan(t, "GET", srv.URL, words)
	if len(params) != 0 {
		t.Fatalf("reflective page produced %d false positives: %v", len(params), params)
	}
	// With the count-axis calibration the scan should conclude in far fewer
	// requests than the ~2x-wordlist blow-up the bug caused. Allow generous slack.
	if sent > int64(len(words)) {
		t.Fatalf("reflective scan sent %d requests for %d words; expansion not contained", sent, len(words))
	}
	t.Logf("reflective scan: %d requests for %d words", sent, len(words))
}

// A page that echoes the NAMES of unknown parameters in an error message (very
// common) is another form of count-scaling and must not blow up either.
func TestScanNameEchoErrorPage(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = r.ParseForm()
		var names []string
		for k := range r.Form {
			names = append(names, k)
		}
		sort.Strings(names)
		fmt.Fprintf(w, "<html><body>Unrecognized parameters: %s</body></html>", strings.Join(names, ", "))
	}))
	defer srv.Close()

	words := baseWords()
	params, _, sent := runScan(t, "GET", srv.URL, words)
	if len(params) != 0 {
		t.Fatalf("name-echo page produced false positives: %v", params)
	}
	if sent > int64(len(words)) {
		t.Fatalf("name-echo scan sent %d requests for %d words; expansion not contained", sent, len(words))
	}
}

// A page that changes status code on a param must be detected via http-code.
func TestScanStatusCodeSignal(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if _, ok := r.URL.Query()["admin"]; ok {
			w.WriteHeader(403)
			return
		}
		fmt.Fprint(w, "<html><body>normal page here</body></html>")
	}))
	defer srv.Close()
	params, status, _ := runScan(t, "GET", srv.URL, baseWords("admin"))
	if status != StatusOK || !eq(params, []string{"admin"}) {
		t.Fatalf("params=%v status=%v, want [admin] OK", params, status)
	}
}

// A fully dynamic page (random content each request) must yield no false positives.
func TestScanDynamicNoFalsePositives(t *testing.T) {
	var n int64
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		c := atomic.AddInt64(&n, 1)
		// Vary length and words unpredictably regardless of params.
		fmt.Fprintf(w, "<html><body>%s %d</body></html>", strings.Repeat("x ", int(c%17)+1), c*7919)
	}))
	defer srv.Close()
	params, _, _ := runScan(t, "GET", srv.URL, baseWords("user_id"))
	if len(params) != 0 {
		t.Fatalf("dynamic page produced false positives: %v", params)
	}
}

// An unreachable target must be skipped, not crash.
func TestScanUnreachable(t *testing.T) {
	_, status, _ := runScan(t, "GET", "http://127.0.0.1:1/nope", baseWords("x"))
	if status != StatusSkipped {
		t.Fatalf("status = %v, want Skipped", status)
	}
}
