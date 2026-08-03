// Package config holds the runtime configuration for a scan run.
package config

import "time"

// Config is the fully-resolved set of options for a run. It is built once in
// main() from CLI flags and then passed (read-only) to the rest of the program.
type Config struct {
	// Targets
	URL        string // single target (-u)
	ImportFile string // file with one URL per line (-i)

	// Wordlist
	Wordlist string // path, or "" / "default" for the embedded list (-w)
	Casing   string // e.g. "like_this", "likeThis", "likethis" (--casing)

	// Request shape
	Method          string            // GET / POST / JSON / XML (-m)
	Headers         map[string]string // extra request headers (--headers)
	Include         map[string]string // params sent with every request (--include)
	DisableRedirect bool              // do not follow redirects (--disable-redirects)
	Timeout         time.Duration     // per-request timeout (-T)

	// Concurrency / pacing
	Workers   int           // concurrent workers (-t)
	Delay     time.Duration // fixed delay between requests (-d)
	Stable    bool          // prefer stability over speed (--stable)
	ChunkSize int           // params per request during narrowing (-c)

	// Adaptive rate limiting (the headline feature)
	RateLimit float64 // starting / maximum requests per second (--rate-limit)
	RateMin   float64 // floor the limiter will not drop below (--rate-min)
	RateAdapt bool    // auto-adjust on rate limiting instead of erroring (default on)
	RateCodes []int   // HTTP status codes treated as "slow down" (--rate-codes)

	// Resilience / resume
	StateFile  string // checkpoint file; enables resumable scans (--state)
	Resume     bool   // load an existing state file and continue (--resume)
	MaxRetries int    // transient network-error retries per request

	// Output
	JSONFile  string // -o / -oJ
	TextFile  string // -oT
	BurpProxy string // route requests through this proxy so they show in Burp (-oB)
	Quiet     bool   // -q
}
