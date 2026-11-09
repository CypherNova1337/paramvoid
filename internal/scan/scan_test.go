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
	"github.com/CypherNova1337/paramvoid/internal/finding"
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

// runScanFull wires up a client + config and scans one URL, returning the full
// result, the status, and the total number of HTTP requests sent.
func runScanFull(t *testing.T, method, url string, words []string) (Result, Status, int64) {
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
	return res, status, client.Sent()
}

// runScan returns just the confirmed param names (sorted), status, and requests.
func runScan(t *testing.T, method, url string, words []string) ([]string, Status, int64) {
	t.Helper()
	res, status, sent := runScanFull(t, method, url, words)
	got := append([]string(nil), res.Params...)
	sort.Strings(got)
	return got, status, sent
}

// findingFor returns the finding for a given param name, or false.
func findingFor(res Result, name string) (finding.Finding, bool) {
	for _, f := range res.Findings {
		if f.Param == name {
			return f, true
		}
	}
	return finding.Finding{}, false
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

// A status-code signal is high-confidence; the finding records the reason.
func TestFindingConfidenceHigh(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if _, ok := r.URL.Query()["admin"]; ok {
			w.WriteHeader(403)
			return
		}
		fmt.Fprint(w, "<html><body>normal page content here</body></html>")
	}))
	defer srv.Close()
	res, status, _ := runScanFull(t, "GET", srv.URL, baseWords("admin"))
	if status != StatusOK {
		t.Fatalf("status = %v", status)
	}
	f, ok := findingFor(res, "admin")
	if !ok {
		t.Fatalf("admin not found; findings=%v", res.Findings)
	}
	if f.Confidence != finding.High || f.Reason != "http-code" {
		t.Fatalf("admin finding = %+v, want high/http-code", f)
	}
}

// A hit on a static asset (robots.txt) is kept but downgraded to low with a note,
// because a static file cannot have a real server-side parameter.
func TestFindingStaticAssetDowngrade(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		extra := ""
		if _, ok := r.URL.Query()["q"]; ok {
			extra = " and more and more and more"
		}
		fmt.Fprintf(w, "User-agent: *\nDisallow:%s\n", extra)
	}))
	defer srv.Close()
	res, _, _ := runScanFull(t, "GET", srv.URL+"/robots.txt", baseWords("q"))
	f, ok := findingFor(res, "q")
	if !ok {
		t.Fatalf("q not found on static asset; findings=%v", res.Findings)
	}
	if f.Confidence != finding.Low {
		t.Fatalf("static-asset finding confidence = %v, want low", f.Confidence)
	}
	hasNote := false
	for _, n := range f.Notes {
		if n == "static-asset" {
			hasNote = true
		}
	}
	if !hasNote {
		t.Fatalf("expected static-asset note, got %v", f.Notes)
	}
}

func TestIsStaticAsset(t *testing.T) {
	static := []string{
		"http://x/robots.txt", "https://x/sitemap.xml", "http://x/a/app.js",
		"http://x/style.css", "http://x/img/logo.png?v=2", "http://x/f.WOFF2",
	}
	dynamic := []string{
		"http://x/", "http://x/overview/default.aspx", "http://x/api/users",
		"http://x/_next/image", "http://x/data.json", "http://x/do.action",
	}
	for _, u := range static {
		if !isStaticAsset(u) {
			t.Errorf("isStaticAsset(%q) = false, want true", u)
		}
	}
	for _, u := range dynamic {
		if isStaticAsset(u) {
			t.Errorf("isStaticAsset(%q) = true, want false", u)
		}
	}
}

func TestHeuristicSkipsFrameworkKeys(t *testing.T) {
	body := `<script id="__NEXT_DATA__" type="application/json">{"props":{"pageProps":{"realParam":1}},"buildId":"abc","query":{}}</script><input name="search_box">`
	got := heuristic(body)
	has := func(s string) bool {
		for _, g := range got {
			if g == s {
				return true
			}
		}
		return false
	}
	if has("pageProps") || has("buildId") || has("__NEXT_DATA__") {
		t.Fatalf("heuristic seeded framework keys: %v", got)
	}
	if !has("realParam") || !has("search_box") {
		t.Fatalf("heuristic dropped real candidates: %v", got)
	}
}
