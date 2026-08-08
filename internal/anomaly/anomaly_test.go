package anomaly

import (
	"net/http"
	"testing"

	"github.com/CypherNova1337/paramvoid/internal/httpx"
)

func resp(code, length, words, lines int, loc string, hdr http.Header, body string) *httpx.Response {
	if hdr == nil {
		hdr = http.Header{}
	}
	return &httpx.Response{
		StatusCode: code,
		Body:       body,
		Header:     hdr,
		Location:   loc,
		Length:     length,
		Words:      words,
		Lines:      lines,
	}
}

func TestDefineTrustsOnlyAgreeingSignals(t *testing.T) {
	r1 := resp(200, 100, 10, 5, "", nil, "abc")
	r2 := resp(200, 100, 10, 5, "", nil, "abc")
	f := Define(r1, []string{"j1"}, r2, []string{"j2"})
	if !f.HasCode || !f.HasLength || !f.HasWords || !f.HasLines || !f.HasRedirect {
		t.Fatalf("expected all length/word/line/code/redirect trusted: %+v", f)
	}
	if !f.ReflectValue {
		t.Fatalf("expected ReflectValue true (junk not reflected)")
	}

	// Disagreeing length/words must not be trusted.
	r2b := resp(200, 200, 20, 5, "", nil, "abc")
	f2 := Define(r1, []string{"j1"}, r2b, []string{"j2"})
	if f2.HasLength || f2.HasWords {
		t.Fatalf("expected length/words untrusted when baselines disagree: %+v", f2)
	}
	if !f2.HasLines || !f2.HasCode {
		t.Fatalf("expected lines/code still trusted: %+v", f2)
	}
}

func TestReflectionBaselineAndDetection(t *testing.T) {
	// Junk value leaks into the body => reflection is NOT a trustworthy signal.
	r1 := resp(200, 100, 10, 5, "", nil, "value j1 here")
	r2 := resp(200, 100, 10, 5, "", nil, "value j2 here")
	f := Define(r1, []string{"j1"}, r2, []string{"j2"})
	if f.ReflectValue {
		t.Fatalf("ReflectValue should be false when junk reflects")
	}

	// Junk does NOT leak => reflection trusted; a request whose value DOES appear
	// is flagged.
	r1 = resp(200, 100, 10, 5, "", nil, "clean body")
	r2 = resp(200, 100, 10, 5, "", nil, "clean body")
	f = Define(r1, []string{"j1"}, r2, []string{"j2"})
	if !f.ReflectValue {
		t.Fatalf("ReflectValue should be true when junk does not reflect")
	}
	hit := resp(200, 100, 10, 5, "", nil, "contains marker abc123 in body")
	if got := Compare(hit, f, []string{"abc123"}); got != Reflection {
		t.Fatalf("Compare = %q, want reflection", got)
	}
}

func TestCompareReturnsFirstDeviation(t *testing.T) {
	r1 := resp(200, 100, 10, 5, "", nil, "x")
	r2 := resp(200, 100, 10, 5, "", nil, "x")
	f := Define(r1, []string{"j1"}, r2, []string{"j2"})

	if got := Compare(resp(200, 100, 10, 5, "", nil, "x"), f, []string{"j3"}); got != None {
		t.Fatalf("identical response should be None, got %q", got)
	}
	if got := Compare(resp(500, 100, 10, 5, "", nil, "x"), f, nil); got != Code {
		t.Fatalf("code change should be Code, got %q", got)
	}
	if got := Compare(resp(200, 999, 10, 5, "", nil, "x"), f, nil); got != Length {
		t.Fatalf("length change should be Length, got %q", got)
	}
	if got := Compare(resp(200, 100, 10, 5, "http://x/", nil, "x"), f, nil); got != Redirect {
		t.Fatalf("redirect change should be Redirect, got %q", got)
	}
}

func TestHeaderFingerprintIgnoresVolatile(t *testing.T) {
	h1 := http.Header{"Content-Type": {"text/html"}, "Date": {"Mon"}, "X-Request-Id": {"a"}}
	h2 := http.Header{"Content-Type": {"text/html"}, "Date": {"Tue"}, "X-Request-Id": {"b"}}
	if headerFP(h1) != headerFP(h2) {
		t.Fatalf("volatile headers should not change fingerprint: %q vs %q", headerFP(h1), headerFP(h2))
	}
	h3 := http.Header{"Content-Type": {"text/html"}, "X-Secret": {"z"}}
	if headerFP(h1) == headerFP(h3) {
		t.Fatalf("a new non-volatile header should change the fingerprint")
	}
}

func TestDisableAndActive(t *testing.T) {
	f := Factors{HasCode: true, HasLength: true, ReflectValue: true}
	if f.Active() != 3 {
		t.Fatalf("Active = %d, want 3", f.Active())
	}
	f.Disable(Length)
	f.Disable(Reflection)
	if f.Active() != 1 || !f.HasCode {
		t.Fatalf("after disabling length+reflection, want only code: %+v", f)
	}
}
