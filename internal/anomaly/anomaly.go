// Package anomaly implements Arjun's core idea: learn what a "normal" response
// looks like from throwaway (junk) parameters, then flag any test request whose
// response deviates from that baseline. A deviation means the server reacted to
// one of the parameters we sent — i.e. it is a real, recognised parameter.
package anomaly

import (
	"sort"
	"strings"

	"github.com/CypherNova1337/paramvoid/internal/httpx"
)

// Reason identifies which signal changed. Empty string means "no anomaly".
type Reason string

const (
	None       Reason = ""
	Code       Reason = "http-code"
	Length     Reason = "body-length"
	Words      Reason = "word-count"
	Lines      Reason = "line-count"
	Redirect   Reason = "redirect"
	Headers    Reason = "headers"
	Reflection Reason = "reflection"
)

// Factors is the fingerprint of a normal response. Each "has*" flag marks a
// signal that was stable across the baseline requests and can therefore be
// trusted. Unstable signals are dropped during Stabilize so dynamic pages don't
// produce false positives.
type Factors struct {
	Code      int
	HasCode   bool
	Length    int
	HasLength bool
	Words     int
	HasWords  bool
	Lines     int
	HasLines  bool

	Redirect    string
	HasRedirect bool

	HeaderFP   string
	HasHeaders bool

	// ReflectValue true => baseline junk values did NOT appear in the body, so
	// any sent value that DOES appear is a strong "real param" signal.
	ReflectValue bool
}

// Active reports how many trusted signals remain. If this hits zero the target
// is too dynamic to fingerprint reliably.
func (f Factors) Active() int {
	n := 0
	for _, b := range []bool{f.HasCode, f.HasLength, f.HasWords, f.HasLines, f.HasRedirect, f.HasHeaders, f.ReflectValue} {
		if b {
			n++
		}
	}
	return n
}

// Define builds factors from two baseline responses (each produced by sending a
// distinct junk parameter). vals1/vals2 are the junk VALUES that were sent, used
// to establish the reflection baseline. A signal is only trusted when both
// baselines agree on it.
func Define(r1 *httpx.Response, vals1 []string, r2 *httpx.Response, vals2 []string) Factors {
	f := Factors{}

	if r1.StatusCode == r2.StatusCode {
		f.Code, f.HasCode = r1.StatusCode, true
	}
	if r1.Length == r2.Length {
		f.Length, f.HasLength = r1.Length, true
	}
	if r1.Words == r2.Words {
		f.Words, f.HasWords = r1.Words, true
	}
	if r1.Lines == r2.Lines {
		f.Lines, f.HasLines = r1.Lines, true
	}
	// Redirect location is trusted even when both are empty: a param that induces
	// a redirect will change "" into a real Location.
	if r1.Location == r2.Location {
		f.Redirect, f.HasRedirect = r1.Location, true
	}

	fp1, fp2 := headerFP(r1.Header), headerFP(r2.Header)
	if fp1 == fp2 {
		f.HeaderFP, f.HasHeaders = fp1, true
	}

	// Reflection baseline: trusted only if NO junk value leaked into either body.
	if !anyContained(r1.Body, vals1) && !anyContained(r2.Body, vals2) {
		f.ReflectValue = true
	}

	return f
}

// Compare checks a response against the factors. sentValues are the values that
// were sent with this request (used for reflection detection). It returns the
// first signal that deviated, or None.
func Compare(r *httpx.Response, f Factors, sentValues []string) Reason {
	if f.HasCode && r.StatusCode != f.Code {
		return Code
	}
	if f.HasRedirect && r.Location != f.Redirect {
		return Redirect
	}
	if f.ReflectValue && anyContained(r.Body, sentValues) {
		return Reflection
	}
	if f.HasHeaders && headerFP(r.Header) != f.HeaderFP {
		return Headers
	}
	if f.HasLength && r.Length != f.Length {
		return Length
	}
	if f.HasWords && r.Words != f.Words {
		return Words
	}
	if f.HasLines && r.Lines != f.Lines {
		return Lines
	}
	return None
}

// Disable turns off the signal named by reason. Used by Stabilize to prune
// signals that fluctuate even on identical requests.
func (f *Factors) Disable(reason Reason) {
	switch reason {
	case Code:
		f.HasCode = false
	case Length:
		f.HasLength = false
	case Words:
		f.HasWords = false
	case Lines:
		f.HasLines = false
	case Redirect:
		f.HasRedirect = false
	case Headers:
		f.HasHeaders = false
	case Reflection:
		f.ReflectValue = false
	}
}

// Volatile headers that legitimately change between identical requests and must
// be excluded from the fingerprint.
var volatileHeaders = map[string]bool{
	"date":             true,
	"age":              true,
	"expires":          true,
	"set-cookie":       true,
	"etag":             true,
	"last-modified":    true,
	"content-length":   true, // tracked separately as body length
	"x-request-id":     true,
	"x-amz-request-id": true,
	"x-amz-id-2":       true,
	"cf-ray":           true,
	"x-served-by":      true,
	"x-timer":          true,
	"x-cache":          true,
	"x-cache-hits":     true,
	"keep-alive":       true,
	"x-runtime":        true,
	"report-to":        true,
	"nel":              true,
}

// headerFP produces a stable fingerprint from the sorted set of non-volatile
// header names.
func headerFP(h map[string][]string) string {
	keys := make([]string, 0, len(h))
	for k := range h {
		lk := strings.ToLower(k)
		if volatileHeaders[lk] {
			continue
		}
		keys = append(keys, lk)
	}
	sort.Strings(keys)
	return strings.Join(keys, ",")
}

func anyContained(body string, values []string) bool {
	for _, v := range values {
		if v != "" && strings.Contains(body, v) {
			return true
		}
	}
	return false
}
