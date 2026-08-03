// Command paramvoid is a resilient HTTP parameter-discovery tool inspired by
// Arjun. It finds hidden/unlinked request parameters by learning a response
// baseline and binary-narrowing a wordlist. Its distinguishing features are an
// adaptive rate limiter (it slows down and retries instead of crashing on HTTP
// 429/503) and resumable scans (checkpoint to disk, continue with --resume).
package main

import (
	"bufio"
	"context"
	"flag"
	"fmt"
	"net/url"
	"os"
	"os/signal"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/CypherNova1337/paramvoid/internal/config"
	"github.com/CypherNova1337/paramvoid/internal/httpx"
	"github.com/CypherNova1337/paramvoid/internal/logx"
	"github.com/CypherNova1337/paramvoid/internal/output"
	"github.com/CypherNova1337/paramvoid/internal/ratelimit"
	"github.com/CypherNova1337/paramvoid/internal/scan"
	"github.com/CypherNova1337/paramvoid/internal/state"
	"github.com/CypherNova1337/paramvoid/internal/wordlist"
)

const version = "1.0.0"

func main() {
	registerFlags()
	flag.Parse()

	if os.Getenv("NO_COLOR") != "" || *flagNoColor {
		logx.SetColor(false)
	}
	logx.SetQuiet(*flagQuiet)

	printBanner()

	cfg, requests, wl, err := buildConfig()
	if err != nil {
		logx.Bad("%v", err)
		os.Exit(1)
	}

	// --- Adaptive limiter + HTTP client ---------------------------------
	limiter := ratelimit.New(cfg.RateLimit, cfg.RateMin, cfg.RateAdapt)
	attachRateLogging(limiter)
	client, err := httpx.New(cfg, limiter)
	if err != nil {
		logx.Bad("%v", err)
		os.Exit(1)
	}

	// --- Resumable state -------------------------------------------------
	st, err := state.Load(cfg.StateFile, cfg.Resume)
	if err != nil {
		logx.Bad("Could not load state file: %v", err)
		os.Exit(1)
	}

	// --- Signal handling (Ctrl-C checkpoints and exits cleanly) ---------
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	var results []scan.Result
	start := time.Now()

	for i, req := range requests {
		if ctx.Err() != nil {
			break
		}
		if st.IsDone(req.URL) {
			logx.Info("Skipping already-completed %s (resumed)", req.URL)
			if saved := st.Get(req.URL); saved != nil && len(saved.Params) > 0 {
				results = append(results, scan.Result{URL: req.URL, Method: saved.Method, Headers: req.Headers, Params: saved.Params})
			}
			continue
		}

		logx.Run("Scanning %d/%d: %s", i+1, len(requests), req.URL)
		res, status := scan.Run(ctx, client, cfg, req, wl, st)

		switch status {
		case scan.StatusOK:
			results = append(results, res)
			logx.Good("Parameters found: %s", strings.Join(res.Params, ", "))
			_ = output.Write(cfg, results) // persist incrementally
		case scan.StatusEmpty:
			logx.Info("No parameters were discovered.")
		case scan.StatusSkipped:
			logx.Bad("Skipped %s due to errors.", req.URL)
		}
	}

	st.Flush()

	if ctx.Err() != nil {
		logx.ClearLine()
		if cfg.StateFile != "" {
			logx.Warn("Interrupted. Progress saved to %s — resume with --resume --state %s", cfg.StateFile, cfg.StateFile)
		} else {
			logx.Warn("Interrupted. (Tip: use --state <file> next time to make scans resumable.)")
		}
	} else if cfg.StateFile != "" {
		st.Remove() // clean up on a fully successful run
	}

	if err := output.Write(cfg, results); err != nil {
		logx.Bad("Failed to write output: %v", err)
	}

	printSummary(results, client.Sent(), time.Since(start))
}

// ---------------------------------------------------------------------------
// Flags
// ---------------------------------------------------------------------------

var (
	flagURL        *string
	flagImport     *string
	flagWordlist   *string
	flagMethod     *string
	flagWorkers    *int
	flagDelay      *float64
	flagTimeout    *float64
	flagChunk      *int
	flagJSON       *string
	flagJSONAlt    *string
	flagText       *string
	flagBurp       *string
	flagHeaders    *string
	flagInclude    *string
	flagNoRedirect *bool
	flagStable     *bool
	flagCasing     *string
	flagQuiet      *bool
	flagRate       *float64
	flagRateMin    *float64
	flagNoAdapt    *bool
	flagRateCodes  *string
	flagState      *string
	flagResume     *bool
	flagRetries    *int
	flagNoColor    *bool
)

func registerFlags() {
	flagURL = strPtr("u", "", "Target URL")
	flagImport = strPtr("i", "", "Import target URLs from file (one per line)")
	flagWordlist = strPtr("w", "default", "Wordlist file path (or 'default' for the built-in list)")
	flagMethod = strPtr("m", "GET", "Request method: GET/POST/JSON/XML")
	flagWorkers = intPtr("t", 15, "Number of concurrent workers")
	flagDelay = floatPtr("d", 0, "Delay between requests in seconds")
	flagTimeout = floatPtr("T", 15, "HTTP request timeout in seconds")
	flagChunk = intPtr("c", 0, "Chunk size (params per request; 0 = auto)")
	flagJSON = strPtr("o", "", "Path for JSON output file")
	flagJSONAlt = strPtr("oJ", "", "Path for JSON output file (alias of -o)")
	flagText = strPtr("oT", "", "Path for text output file")
	flagBurp = strPtr("oB", "", "Route requests through this proxy, e.g. 127.0.0.1:8080 (Burp)")
	flagHeaders = strPtr("headers", "", "Extra headers; separate with newlines or \\n")
	flagInclude = strPtr("include", "", "Params to include in every request, e.g. 'a=b&c=d'")
	flagNoRedirect = boolPtr("disable-redirects", false, "Do not follow redirects")
	flagStable = boolPtr("stable", false, "Prefer stability over speed (single worker)")
	flagCasing = strPtr("casing", "", "Rewrite params to a casing style, e.g. like_this / likeThis / likethis")
	flagQuiet = boolPtr("q", false, "Quiet mode; suppress status output")
	flagRate = floatPtr("rate-limit", 20, "Starting/maximum requests per second")
	flagRateMin = floatPtr("rate-min", 1, "Floor the adaptive limiter will not drop below")
	flagNoAdapt = boolPtr("no-adapt", false, "Disable adaptive rate adjustment (fixed rate)")
	flagRateCodes = strPtr("rate-codes", "429,503", "Comma-separated HTTP codes treated as rate limiting")
	flagState = strPtr("state", "", "Checkpoint file enabling resumable scans")
	flagResume = boolPtr("resume", false, "Resume from the --state checkpoint file")
	flagRetries = intPtr("retries", 3, "Transient network-error retries per request")
	flagNoColor = boolPtr("no-color", false, "Disable colored output")
}

// buildConfig resolves flags into a Config, the request list, and the wordlist.
func buildConfig() (*config.Config, []httpx.Request, []string, error) {
	if *flagURL == "" && *flagImport == "" {
		return nil, nil, nil, fmt.Errorf("no target specified (use -u <url> or -i <file>)")
	}

	method := strings.ToUpper(strings.TrimSpace(*flagMethod))
	switch method {
	case "GET", "POST", "JSON", "XML":
	default:
		return nil, nil, nil, fmt.Errorf("invalid method %q (use GET/POST/JSON/XML)", *flagMethod)
	}

	chunk := *flagChunk
	if chunk <= 0 {
		if method == "GET" {
			chunk = 250
		} else {
			chunk = 500
		}
	}

	workers := *flagWorkers
	delay := time.Duration(*flagDelay * float64(time.Second))
	if *flagStable || delay > 0 {
		workers = 1
	}
	if workers < 1 {
		workers = 1
	}

	rateCodes, err := parseCodes(*flagRateCodes)
	if err != nil {
		return nil, nil, nil, err
	}

	jsonFile := *flagJSON
	if jsonFile == "" {
		jsonFile = *flagJSONAlt
	}
	textFile := *flagText
	// Always produce a single consolidated output file, even if the user didn't
	// ask for one — bulk scans should never finish with results only on screen.
	if jsonFile == "" && textFile == "" {
		jsonFile = "paramvoid_output.json"
	}

	cfg := &config.Config{
		URL:             *flagURL,
		ImportFile:      *flagImport,
		Wordlist:        *flagWordlist,
		Casing:          *flagCasing,
		Method:          method,
		Headers:         parseHeaders(*flagHeaders),
		Include:         parseInclude(*flagInclude),
		DisableRedirect: *flagNoRedirect,
		Timeout:         time.Duration(*flagTimeout * float64(time.Second)),
		Workers:         workers,
		Delay:           delay,
		Stable:          *flagStable,
		ChunkSize:       chunk,
		RateLimit:       *flagRate,
		RateMin:         *flagRateMin,
		RateAdapt:       !*flagNoAdapt,
		RateCodes:       rateCodes,
		StateFile:       *flagState,
		Resume:          *flagResume,
		MaxRetries:      *flagRetries,
		JSONFile:        jsonFile,
		TextFile:        textFile,
		BurpProxy:       *flagBurp,
		Quiet:           *flagQuiet,
	}

	// Fold a fixed delay into the limiter ceiling.
	if cfg.Delay > 0 {
		perSec := 1.0 / cfg.Delay.Seconds()
		if perSec < cfg.RateLimit {
			cfg.RateLimit = perSec
		}
	}

	// Wordlist
	wl, err := wordlist.Load(cfg.Wordlist)
	if err != nil {
		return nil, nil, nil, fmt.Errorf("loading wordlist: %w", err)
	}
	if len(wl) == 0 {
		return nil, nil, nil, fmt.Errorf("wordlist is empty")
	}
	if cfg.Casing != "" {
		wl = wordlist.Apply(wl, wordlist.DetectStyle(cfg.Casing))
	}
	// Keep chunk size sane relative to wordlist size.
	if cfg.ChunkSize > len(wl) {
		cfg.ChunkSize = (len(wl) + 1) / 2
		if cfg.ChunkSize < 1 {
			cfg.ChunkSize = 1
		}
	}

	// Build request list (URLs are auto-corrected: scheme added if missing,
	// whitespace/quotes trimmed, malformed lines skipped, duplicates removed).
	var requests []httpx.Request
	seen := map[string]bool{}
	add := func(raw string) bool {
		nu, ok := normalizeURL(raw)
		if !ok || seen[nu] {
			return ok // ok==false means malformed; already-seen is silently fine
		}
		seen[nu] = true
		requests = append(requests, httpx.Request{URL: nu, Method: method, Headers: cfg.Headers})
		return true
	}

	if cfg.URL != "" {
		nu, ok := normalizeURL(cfg.URL)
		if !ok {
			return nil, nil, nil, fmt.Errorf("invalid target URL %q", cfg.URL)
		}
		cfg.URL = nu
		add(nu)
	}
	if cfg.ImportFile != "" {
		lines, err := readLines(cfg.ImportFile)
		if err != nil {
			return nil, nil, nil, fmt.Errorf("reading import file: %w", err)
		}
		var skipped int
		for _, line := range lines {
			if !add(line) {
				skipped++
			}
		}
		if skipped > 0 {
			logx.Warn("Skipped %d malformed line(s) in %s", skipped, cfg.ImportFile)
		}
	}
	if len(requests) == 0 {
		return nil, nil, nil, fmt.Errorf("no valid targets to scan")
	}

	return cfg, requests, wl, nil
}

// ---------------------------------------------------------------------------
// Small helpers
// ---------------------------------------------------------------------------

func parseHeaders(s string) map[string]string {
	out := map[string]string{}
	if s == "" {
		return out
	}
	normalized := strings.ReplaceAll(s, "\\n", "\n")
	for _, line := range strings.Split(normalized, "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		if idx := strings.Index(line, ":"); idx > 0 {
			k := strings.TrimSpace(line[:idx])
			v := strings.TrimSpace(line[idx+1:])
			if k != "" {
				out[k] = v
			}
		}
	}
	return out
}

func parseInclude(s string) map[string]string {
	out := map[string]string{}
	if s == "" {
		return out
	}
	if vals, err := url.ParseQuery(s); err == nil {
		for k, v := range vals {
			if len(v) > 0 {
				out[k] = v[0]
			}
		}
	}
	return out
}

func parseCodes(s string) ([]int, error) {
	var codes []int
	for _, part := range strings.Split(s, ",") {
		part = strings.TrimSpace(part)
		if part == "" {
			continue
		}
		n, err := strconv.Atoi(part)
		if err != nil {
			return nil, fmt.Errorf("invalid rate code %q", part)
		}
		codes = append(codes, n)
	}
	return codes, nil
}

// normalizeURL cleans a raw target URL for scanning: it trims whitespace and
// surrounding quotes, prepends https:// when no scheme is present, and verifies
// the result has a host. ok=false means the line is unusable and should be
// skipped. This lets a list freely mix "example.com", "http://x/y", and
// quoted/padded entries without the scan choking on them.
func normalizeURL(raw string) (string, bool) {
	s := strings.TrimSpace(raw)
	s = strings.Trim(s, "\"'")
	s = strings.TrimSpace(s)
	if s == "" || strings.HasPrefix(s, "#") {
		return "", false
	}
	if !strings.Contains(s, "://") {
		s = "https://" + s
	}
	u, err := url.Parse(s)
	if err != nil || u.Host == "" {
		return "", false
	}
	if u.Scheme != "http" && u.Scheme != "https" {
		return "", false
	}
	return u.String(), true
}

func readLines(path string) ([]string, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	var lines []string
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 0, 64*1024), 1024*1024)
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if line != "" && !strings.HasPrefix(line, "#") {
			lines = append(lines, line)
		}
	}
	return lines, sc.Err()
}

// attachRateLogging wires the limiter's adjustment callback to throttled logging.
func attachRateLogging(l *ratelimit.Limiter) {
	var (
		mu   sync.Mutex
		last time.Time
	)
	l.OnAdjust(func(rate float64, reason string) {
		mu.Lock()
		defer mu.Unlock()
		if time.Since(last) < time.Second { // avoid log spam under sustained 429s
			return
		}
		last = time.Now()
		switch reason {
		case "rate-limit":
			logx.Warn("Rate limiting detected — backing off, now ~%.1f req/s (auto-adjusting, not crashing)", rate)
		case "network":
			logx.Warn("Network hiccup — slowing to ~%.1f req/s and retrying", rate)
		case "recover":
			logx.Info("Target looks healthy — ramping back up to ~%.1f req/s", rate)
		}
	})
}

func printBanner() {
	if *flagQuiet {
		return
	}
	fmt.Fprintf(os.Stderr, `%s    _
   /_| _ '
  (  |/ /(//)  paramvoid v%s
      _/       resilient HTTP parameter discovery%s

`, logx.Green, version, logx.End)
}

func printSummary(results []scan.Result, sent int64, elapsed time.Duration) {
	if *flagQuiet {
		return
	}
	total := 0
	for _, r := range results {
		total += len(r.Params)
	}
	logx.Info("Done in %s — %d request(s) sent, %d parameter(s) across %d URL(s).",
		elapsed.Round(time.Millisecond), sent, total, len(results))
}
