package main

import (
	"testing"

	"github.com/CypherNova1337/paramvoid/internal/finding"
	"github.com/CypherNova1337/paramvoid/internal/scan"
)

func mkResult(url string, params ...string) scan.Result {
	r := scan.Result{URL: url, Method: "GET", Params: params}
	for _, p := range params {
		r.Findings = append(r.Findings, finding.Finding{Param: p, Reason: "body-length", Confidence: finding.Medium})
	}
	return r
}

// markSiteWide must downgrade a param that recurs across most of a host's URLs,
// while leaving a param that appears on just one page alone.
func TestMarkSiteWide(t *testing.T) {
	var results []scan.Result
	// 6 pages on one host all expose "state"; only one also exposes "search".
	for i, path := range []string{"/a", "/b", "/c", "/d", "/e", "/f"} {
		if i == 0 {
			results = append(results, mkResult("https://core.example.com"+path, "state", "search"))
		} else {
			results = append(results, mkResult("https://core.example.com"+path, "state"))
		}
	}
	// A different host with few pages must be untouched.
	results = append(results, mkResult("https://api.example.com/x", "token"))

	markSiteWide(results)

	for _, r := range results {
		for _, f := range r.Findings {
			switch f.Param {
			case "state":
				if f.Confidence != finding.Low || !hasNote(f, "site-wide") {
					t.Errorf("state should be low+site-wide, got %+v", f)
				}
			case "search", "token":
				if f.Confidence == finding.Low || hasNote(f, "site-wide") {
					t.Errorf("%s should NOT be site-wide, got %+v", f.Param, f)
				}
			}
		}
	}
}

func hasNote(f finding.Finding, n string) bool {
	for _, x := range f.Notes {
		if x == n {
			return true
		}
	}
	return false
}

func TestHostOf(t *testing.T) {
	cases := map[string]string{
		"https://core.verisk.com/x":     "core.verisk.com",
		"http://core.verisk.com:8080/y": "core.verisk.com",
		"https://a.b.c/path?q=1":        "a.b.c",
	}
	for in, want := range cases {
		if got := hostOf(in); got != want {
			t.Errorf("hostOf(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestNormalizeURL(t *testing.T) {
	cases := []struct {
		in   string
		want string
		ok   bool
	}{
		{"example.com", "https://example.com", true},
		{"  example.com/api  ", "https://example.com/api", true},
		{`"http://x.tld/y"`, "http://x.tld/y", true},
		{"https://a.tld/p?q=1", "https://a.tld/p?q=1", true},
		{"# comment", "", false},
		{"", "", false},
		{"http://", "", false},
		{"ftp://host/x", "", false},
		{"javascript:alert(1)", "", false},
	}
	for _, c := range cases {
		got, ok := normalizeURL(c.in)
		if ok != c.ok || (ok && got != c.want) {
			t.Errorf("normalizeURL(%q) = (%q,%v), want (%q,%v)", c.in, got, ok, c.want, c.ok)
		}
	}
}

func TestParseHeaders(t *testing.T) {
	h := parseHeaders(`X-A: 1\nAuthorization: Bearer z`)
	if h["X-A"] != "1" || h["Authorization"] != "Bearer z" {
		t.Fatalf("parseHeaders got %v", h)
	}
	// Real newlines also work; blank and malformed lines are skipped.
	h = parseHeaders("A: b\n\nnokeyhere\n: noname\nC: d")
	if h["A"] != "b" || h["C"] != "d" {
		t.Fatalf("parseHeaders got %v", h)
	}
	if len(h) != 2 {
		t.Fatalf("expected 2 valid headers, got %v", h)
	}
}

func TestParseInclude(t *testing.T) {
	inc := parseInclude("a=b&c=d&e=")
	if inc["a"] != "b" || inc["c"] != "d" {
		t.Fatalf("parseInclude got %v", inc)
	}
}

func TestParseCodes(t *testing.T) {
	codes, err := parseCodes("429, 503 ,418")
	if err != nil {
		t.Fatalf("parseCodes err: %v", err)
	}
	if len(codes) != 3 || codes[0] != 429 || codes[2] != 418 {
		t.Fatalf("parseCodes got %v", codes)
	}
	if _, err := parseCodes("429,notanumber"); err == nil {
		t.Fatalf("expected error on bad code")
	}
}
