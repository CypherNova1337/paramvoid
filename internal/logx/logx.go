// Package logx provides small, concurrency-safe, colorized status logging that
// mirrors Arjun's [*]/[+]/[-] prefix style and honours a global quiet mode.
package logx

import (
	"fmt"
	"os"
	"sync"
)

// ANSI colors (exported so other packages can highlight values consistently).
const (
	Green  = "\033[92m"
	Red    = "\033[91m"
	Blue   = "\033[94m"
	Cyan   = "\033[96m"
	Yellow = "\033[93m"
	Gray   = "\033[90m"
	Bold   = "\033[1m"
	End    = "\033[0m"
)

var (
	mu       sync.Mutex
	quiet    bool
	color    = true
	progress = true
)

// SetQuiet suppresses all output when q is true.
func SetQuiet(q bool) { mu.Lock(); quiet = q; mu.Unlock() }

// SetColor toggles ANSI coloring (disable for non-tty / file logging).
func SetColor(c bool) { mu.Lock(); color = c; mu.Unlock() }

// SetProgress toggles the in-place progress counter. Disable it when stderr is
// not a terminal so redirected logs and files don't fill up with carriage
// returns and half-overwritten counter lines.
func SetProgress(p bool) { mu.Lock(); progress = p; mu.Unlock() }

// Paint wraps s in the given ANSI color code when coloring is enabled, and
// returns s unchanged otherwise. Use it for inline highlights inside a message
// (e.g. a discovered parameter name) so they honour --no-color and the non-tty
// auto-detection instead of hardcoding escape codes at the call site.
func Paint(code, s string) string {
	mu.Lock()
	on := color
	mu.Unlock()
	if !on {
		return s
	}
	return code + s + End
}

func emit(prefix, colorCode, format string, a ...any) {
	mu.Lock()
	defer mu.Unlock()
	if quiet {
		return
	}
	msg := fmt.Sprintf(format, a...)
	if color {
		fmt.Fprintf(os.Stderr, "%s%s%s %s\n", colorCode, prefix, End, msg)
	} else {
		fmt.Fprintf(os.Stderr, "%s %s\n", prefix, msg)
	}
}

// Info is a neutral status line: [*]
func Info(format string, a ...any) { emit("[*]", Blue, format, a...) }

// Good reports success: [+]
func Good(format string, a ...any) { emit("[+]", Green, format, a...) }

// Bad reports a problem: [-]
func Bad(format string, a ...any) { emit("[-]", Red, format, a...) }

// Run reports work in progress: [~]
func Run(format string, a ...any) { emit("[~]", Yellow, format, a...) }

// Res reports a discovery result: [<]
func Res(format string, a ...any) { emit("[<]", Cyan, format, a...) }

// Warn reports an adaptive adjustment (rate limiting, backoff): [!]
func Warn(format string, a ...any) { emit("[!]", Yellow, format, a...) }

// Progress overwrites the current line (no newline). Used for chunk counters.
// It is a no-op when quiet or when the progress counter is disabled (non-tty).
func Progress(format string, a ...any) {
	mu.Lock()
	defer mu.Unlock()
	if quiet || !progress {
		return
	}
	fmt.Fprintf(os.Stderr, "\r"+format, a...)
}

// ClearLine clears a Progress line so the next message starts clean.
func ClearLine() {
	mu.Lock()
	defer mu.Unlock()
	if quiet || !progress {
		return
	}
	fmt.Fprint(os.Stderr, "\r\033[K")
}
