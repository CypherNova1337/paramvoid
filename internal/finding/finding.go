// Package finding describes a confirmed parameter together with WHY it was
// confirmed and how much to trust it. paramvoid confirms a parameter when the
// server's response reacts to it, but not all reactions are equal: a changed
// status code or redirect is a strong signal the server branches on the
// parameter, whereas a value simply echoed back into the page is weak — it is
// how SPA frameworks (Next.js and friends) treat *every* query key, so it
// produces a lot of noise on modern targets. Recording the reason and a
// confidence tier lets a bulk scan separate real handlers from framework echo
// without discarding the echo (a reflected value is still an XSS lead).
package finding

// Confidence grades how much a confirmation is worth acting on.
type Confidence string

const (
	High   Confidence = "high"   // status-code / redirect change: the server branches on it
	Medium Confidence = "medium" // body/header changed in a way not explained by echo alone
	Low    Confidence = "low"    // reflection-only, static asset, or site-wide echo: likely framework noise
)

// Finding is one confirmed parameter for one URL.
type Finding struct {
	Param      string     `json:"param"`
	Reason     string     `json:"reason"`          // anomaly signal, e.g. "http-code", "reflection"
	Confidence Confidence `json:"confidence"`      // high / medium / low
	Notes      []string   `json:"notes,omitempty"` // downgrade reasons, e.g. "static-asset", "site-wide"
}

// ForReason maps a detection reason to a baseline confidence tier.
func ForReason(reason string) Confidence {
	switch reason {
	case "http-code", "redirect":
		return High
	case "reflection":
		return Low
	default: // body-length, word-count, line-count, headers
		return Medium
	}
}

// Note adds an annotation once (deduplicated), used when a modifier downgrades a
// finding's confidence.
func (f *Finding) Note(n string) {
	for _, existing := range f.Notes {
		if existing == n {
			return
		}
	}
	f.Notes = append(f.Notes, n)
}

// Names returns just the parameter names from a slice of findings, preserving
// order — used to keep the Arjun-compatible `params` list alongside the richer
// findings.
func Names(fs []Finding) []string {
	out := make([]string, 0, len(fs))
	for _, f := range fs {
		out = append(out, f.Param)
	}
	return out
}
