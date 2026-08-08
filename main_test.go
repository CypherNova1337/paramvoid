package main

import "testing"

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
