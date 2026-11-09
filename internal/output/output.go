// Package output writes scan results to JSON and/or text files in a format
// compatible with Arjun's JSON layout: { url: { params, method, headers } }.
package output

import (
	"encoding/json"
	"fmt"
	"os"
	"strings"

	"github.com/CypherNova1337/paramvoid/internal/config"
	"github.com/CypherNova1337/paramvoid/internal/finding"
	"github.com/CypherNova1337/paramvoid/internal/scan"
)

type entry struct {
	Params   []string          `json:"params"` // names only, kept Arjun-compatible
	Method   string            `json:"method"`
	Findings []finding.Finding `json:"findings,omitempty"` // per-param reason + confidence
	Headers  map[string]string `json:"headers,omitempty"`
}

// Write persists results to the configured JSON/text files (if any). It is safe
// to call repeatedly (rewrites the full file each time) so results survive an
// interruption mid-run.
//
// The JSON `params` list stays a plain array of names so existing Arjun-style
// tooling keeps working; the richer per-parameter detail (reason + confidence)
// is added alongside as `findings`. The text file gains a 4th tab-separated
// column for confidence, appended so `$1/$2/$3` consumers are unaffected.
func Write(cfg *config.Config, results []scan.Result) error {
	if cfg.JSONFile != "" {
		out := make(map[string]entry, len(results))
		for _, r := range results {
			out[r.URL] = entry{Params: r.Params, Method: r.Method, Findings: r.Findings, Headers: r.Headers}
		}
		b, err := json.MarshalIndent(out, "", "  ")
		if err != nil {
			return err
		}
		if err := os.WriteFile(cfg.JSONFile, b, 0o644); err != nil {
			return err
		}
	}

	if cfg.TextFile != "" {
		var b strings.Builder
		for _, r := range results {
			conf := confidenceByParam(r.Findings)
			for _, p := range r.Params {
				c := conf[p]
				if c == "" {
					c = "-"
				}
				fmt.Fprintf(&b, "%s\t%s\t%s\t%s\n", r.URL, r.Method, p, c)
			}
		}
		if err := os.WriteFile(cfg.TextFile, []byte(b.String()), 0o644); err != nil {
			return err
		}
	}
	return nil
}

// confidenceByParam indexes a result's findings by parameter name, including any
// downgrade notes so the text column reads e.g. "low (site-wide)".
func confidenceByParam(fs []finding.Finding) map[string]string {
	m := make(map[string]string, len(fs))
	for _, f := range fs {
		s := string(f.Confidence)
		if len(f.Notes) > 0 {
			s += " (" + strings.Join(f.Notes, ",") + ")"
		}
		m[f.Param] = s
	}
	return m
}
