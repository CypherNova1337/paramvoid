// Package output writes scan results to JSON and/or text files in a format
// compatible with Arjun's JSON layout: { url: { params, method, headers } }.
package output

import (
	"encoding/json"
	"fmt"
	"os"
	"strings"

	"github.com/CypherNova1337/paramvoid/internal/config"
	"github.com/CypherNova1337/paramvoid/internal/scan"
)

type entry struct {
	Params  []string          `json:"params"`
	Method  string            `json:"method"`
	Headers map[string]string `json:"headers,omitempty"`
}

// Write persists results to the configured JSON/text files (if any). It is safe
// to call repeatedly (rewrites the full file each time) so results survive an
// interruption mid-run.
func Write(cfg *config.Config, results []scan.Result) error {
	if cfg.JSONFile != "" {
		out := make(map[string]entry, len(results))
		for _, r := range results {
			out[r.URL] = entry{Params: r.Params, Method: r.Method, Headers: r.Headers}
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
			for _, p := range r.Params {
				fmt.Fprintf(&b, "%s\t%s\t%s\n", r.URL, r.Method, p)
			}
		}
		if err := os.WriteFile(cfg.TextFile, []byte(b.String()), 0o644); err != nil {
			return err
		}
	}
	return nil
}
