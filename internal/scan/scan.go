// Package scan implements the parameter-discovery algorithm: establish a
// response baseline, learn which signals are stable, then binary-narrow a
// wordlist down to the parameters the server actually reacts to, and finally
// verify each candidate individually. All network access goes through the
// adaptive client, so rate limiting and transient errors are handled below this
// layer and never abort a scan.
package scan

import (
	"context"
	"math/rand"
	"regexp"
	"sort"
	"strings"
	"sync"
	"sync/atomic"

	"github.com/CypherNova1337/paramvoid/internal/anomaly"
	"github.com/CypherNova1337/paramvoid/internal/config"
	"github.com/CypherNova1337/paramvoid/internal/httpx"
	"github.com/CypherNova1337/paramvoid/internal/logx"
	"github.com/CypherNova1337/paramvoid/internal/state"
)

// Result is the set of confirmed parameters for one URL.
type Result struct {
	URL     string
	Method  string
	Headers map[string]string
	Params  []string
}

// Status describes the outcome of scanning one URL.
type Status string

const (
	StatusOK      Status = "ok"      // parameters were found
	StatusEmpty   Status = "empty"   // scan completed, none found
	StatusSkipped Status = "skipped" // could not scan (bad URL, unreachable, too dynamic)
)

type engine struct {
	ctx     context.Context
	client  *httpx.Client
	cfg     *config.Config
	req     httpx.Request
	factors anomaly.Factors
}

// Run scans a single request/URL and returns its confirmed parameters.
func Run(ctx context.Context, client *httpx.Client, cfg *config.Config, req httpx.Request, words []string, st *state.State) (Result, Status) {
	res := Result{URL: req.URL, Method: strings.ToUpper(req.Method), Headers: req.Headers}

	if !strings.HasPrefix(req.URL, "http") {
		logx.Bad("%s is not a valid URL", req.URL)
		return res, StatusSkipped
	}

	e := &engine{ctx: ctx, client: client, cfg: cfg, req: req}

	// --- Stability probe -------------------------------------------------
	logx.Run("Probing the target for stability")
	if _, err := client.Do(ctx, req, nil); err != nil {
		logx.Bad("Target unreachable: %v", err)
		return res, StatusSkipped
	}

	// --- Baseline & factors ---------------------------------------------
	r1, v1, err1 := e.send([]string{randName()})
	r2, v2, err2 := e.send([]string{randName()})
	if err1 != nil || err2 != nil || r1 == nil || r2 == nil {
		logx.Bad("Could not establish a baseline for %s", req.URL)
		return res, StatusSkipped
	}
	if r1.RateLimited || r2.RateLimited {
		logx.Bad("Target is rate limiting the baseline requests; try a lower --rate-limit. Skipping %s", req.URL)
		return res, StatusSkipped
	}
	e.factors = anomaly.Define(r1, v1, r2, v2)

	// Prune signals that fluctuate even on identical junk requests.
	for i := 0; i < 8 && e.factors.Active() > 0; i++ {
		rj, vj, err := e.send([]string{randName()})
		if err != nil || rj == nil {
			break
		}
		reason := anomaly.Compare(rj, e.factors, vj)
		if reason == anomaly.None {
			break
		}
		e.factors.Disable(reason)
	}
	if e.factors.Active() == 0 {
		logx.Bad("Target response is too dynamic to fingerprint; skipping %s", req.URL)
		return res, StatusSkipped
	}
	logx.Info("Analysing HTTP response for anomalies")

	// --- Heuristic extraction from the baseline body --------------------
	extracted := heuristic(r1.Body)
	if len(extracted) > 0 {
		logx.Good("Extracted %d parameter name(s) from the response for priority testing", len(extracted))
	}

	// --- Build (or resume) the narrowing queue --------------------------
	var (
		queue      [][]string
		candidates []string
	)
	if saved := st.Get(req.URL); saved != nil && len(saved.Pending) > 0 {
		queue = saved.Pending
		candidates = append(candidates, saved.Candidates...)
		logx.Info("Resuming %s with %d pending chunk(s)", req.URL, len(queue))
	} else {
		for _, name := range extracted {
			queue = append(queue, []string{name}) // priority singletons first
		}
		queue = append(queue, chunkify(words, cfg.ChunkSize)...)
	}

	// --- Narrowing (binary search over anomalous chunks) ----------------
	logx.Run("Logicforcing the endpoint")
	prevCount := len(queue)
	const maxLevels = 64
	for level := 0; len(queue) > 0; level++ {
		if ctx.Err() != nil {
			st.SaveProgress(req.URL, res.Method, queue, candidates, true)
			return res, StatusSkipped
		}
		if level > maxLevels {
			logx.Warn("Reached narrowing depth limit; proceeding with %d candidate(s)", len(candidates))
			break
		}

		splits, singles := e.processLevel(queue)
		candidates = append(candidates, singles...)

		// If the queue grew, sanity-check that the page isn't simply dynamic.
		if len(splits) > prevCount && e.pageIsDynamic() {
			logx.ClearLine()
			logx.Bad("Target returns different content on each request; skipping %s", req.URL)
			return res, StatusEmpty
		}
		prevCount = len(splits)
		queue = splits
		st.SaveProgress(req.URL, res.Method, queue, candidates, false)
	}
	logx.ClearLine()

	// --- Verification ----------------------------------------------------
	confirmed := e.verify(dedupe(candidates))
	res.Params = confirmed
	st.MarkDone(req.URL, res.Method, confirmed)

	if len(confirmed) == 0 {
		return res, StatusEmpty
	}
	return res, StatusOK
}

// send issues a request with the given parameter NAMES, assigning each a fresh
// random value, and returns the response plus the values sent (for reflection
// detection).
func (e *engine) send(names []string) (*httpx.Response, []string, error) {
	params := make(map[string]string, len(names))
	vals := make([]string, 0, len(names))
	for _, n := range names {
		v := randValue()
		params[n] = v
		vals = append(vals, v)
	}
	resp, err := e.client.Do(e.ctx, e.req, params)
	return resp, vals, err
}

// processLevel tests every chunk in the current level concurrently. Chunks whose
// response deviates from the baseline are split in half (or recorded as a single
// candidate); chunks with no deviation are discarded. Chunks that error are
// re-queued unchanged so nothing is silently lost.
func (e *engine) processLevel(queue [][]string) (splits [][]string, singles []string) {
	var mu sync.Mutex
	var wg sync.WaitGroup

	workers := e.cfg.Workers
	if workers < 1 {
		workers = 1
	}
	jobs := make(chan []string)

	total := int32(len(queue))
	var done int32

	for w := 0; w < workers; w++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for chunk := range jobs {
				if e.ctx.Err() != nil {
					return
				}
				// The inner func lets `return` act like the old per-chunk
				// `continue` while still reaching the progress update below.
				func() {
					resp, vals, err := e.send(chunk)
					if err != nil || resp == nil || resp.RateLimited {
						mu.Lock()
						splits = append(splits, chunk) // inconclusive: retry later
						mu.Unlock()
						return
					}
					if anomaly.Compare(resp, e.factors, vals) == anomaly.None {
						return
					}
					mu.Lock()
					if len(chunk) == 1 {
						singles = append(singles, chunk[0])
					} else {
						a, b := splitHalf(chunk)
						splits = append(splits, a, b)
					}
					mu.Unlock()
				}()
				n := atomic.AddInt32(&done, 1)
				logx.Progress("%s[!]%s Processing chunks: %d/%d ", logx.Yellow, logx.End, n, total)
			}
		}()
	}

	for _, chunk := range queue {
		if e.ctx.Err() != nil {
			break
		}
		jobs <- chunk
	}
	close(jobs)
	wg.Wait()
	return splits, singles
}

// verify re-tests each candidate individually to filter out flukes. A candidate
// is confirmed only if it triggers a consistent anomaly across two checks.
func (e *engine) verify(names []string) []string {
	var confirmed []string
	single := e.cfg.URL != "" && e.cfg.ImportFile == "" // verbose per-param output for single-URL runs
	for _, name := range names {
		if e.ctx.Err() != nil {
			break
		}
		r1, v1, err1 := e.send([]string{name})
		if err1 != nil || r1 == nil || r1.RateLimited {
			continue
		}
		reason1 := anomaly.Compare(r1, e.factors, v1)
		if reason1 == anomaly.None {
			continue
		}
		r2, v2, err2 := e.send([]string{name})
		if err2 != nil || r2 == nil || r2.RateLimited {
			continue
		}
		if anomaly.Compare(r2, e.factors, v2) == anomaly.None {
			continue
		}
		confirmed = append(confirmed, name)
		if single {
			logx.Res("parameter detected: %s%s%s (based on: %s)", logx.Bold, name, logx.End, reason1)
		}
	}
	return confirmed
}

// pageIsDynamic returns true if a junk request now deviates from the learned
// baseline, i.e. the page changes content between identical requests.
func (e *engine) pageIsDynamic() bool {
	for i := 0; i < 2; i++ {
		resp, vals, err := e.send([]string{randName()})
		if err != nil || resp == nil || resp.RateLimited {
			return false // can't determine; don't falsely abort
		}
		if anomaly.Compare(resp, e.factors, vals) != anomaly.None {
			return true
		}
	}
	return false
}

// ---------------------------------------------------------------------------
// Helpers
// ---------------------------------------------------------------------------

func chunkify(words []string, size int) [][]string {
	if size < 1 {
		size = 1
	}
	var chunks [][]string
	for i := 0; i < len(words); i += size {
		end := i + size
		if end > len(words) {
			end = len(words)
		}
		chunk := make([]string, end-i)
		copy(chunk, words[i:end])
		chunks = append(chunks, chunk)
	}
	return chunks
}

func splitHalf(chunk []string) ([]string, []string) {
	mid := len(chunk) / 2
	if mid == 0 {
		mid = 1
	}
	a := make([]string, mid)
	b := make([]string, len(chunk)-mid)
	copy(a, chunk[:mid])
	copy(b, chunk[mid:])
	return a, b
}

func dedupe(in []string) []string {
	seen := make(map[string]bool, len(in))
	var out []string
	for _, s := range in {
		if !seen[s] {
			seen[s] = true
			out = append(out, s)
		}
	}
	sort.Strings(out)
	return out
}

const valueCharset = "abcdefghijklmnopqrstuvwxyz0123456789"

// randValue returns a random reflection marker unlikely to occur naturally.
func randValue() string {
	b := make([]byte, 7)
	for i := range b {
		b[i] = valueCharset[rand.Intn(len(valueCharset))]
	}
	return "z" + string(b)
}

// randName returns a random junk parameter name (used to learn the baseline).
func randName() string {
	const letters = "abcdefghijklmnopqrstuvwxyz"
	b := make([]byte, 8)
	for i := range b {
		b[i] = letters[rand.Intn(len(letters))]
	}
	return string(b)
}

// heuristic extracts plausible parameter names from an HTML/JSON body (form
// input names, id attributes, and JSON keys) to test with priority.
var (
	reNameAttr = regexp.MustCompile(`(?i)\b(?:name|id)\s*=\s*["']([a-zA-Z0-9_\-]{1,40})["']`)
	reJSONKey  = regexp.MustCompile(`["']([a-zA-Z0-9_\-]{1,40})["']\s*:`)
)

func heuristic(body string) []string {
	if len(body) == 0 {
		return nil
	}
	seen := make(map[string]bool)
	var out []string
	add := func(matches [][]string) {
		for _, m := range matches {
			if len(m) < 2 {
				continue
			}
			name := m[1]
			if name == "" || seen[name] {
				continue
			}
			seen[name] = true
			out = append(out, name)
			if len(out) >= 60 { // cap priority list
				return
			}
		}
	}
	add(reNameAttr.FindAllStringSubmatch(body, -1))
	add(reJSONKey.FindAllStringSubmatch(body, -1))
	return out
}
