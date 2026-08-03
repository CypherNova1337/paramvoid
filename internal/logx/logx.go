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
	mu    sync.Mutex
	quiet bool
	color = true
)

// SetQuiet suppresses all output when q is true.
func SetQuiet(q bool) { mu.Lock(); quiet = q; mu.Unlock() }

// SetColor toggles ANSI coloring (disable for non-tty / file logging).
func SetColor(c bool) { mu.Lock(); color = c; mu.Unlock() }

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
func Progress(format string, a ...any) {
	mu.Lock()
	defer mu.Unlock()
	if quiet {
		return
	}
	fmt.Fprintf(os.Stderr, "\r"+format, a...)
}

// ClearLine clears a Progress line so the next message starts clean.
func ClearLine() {
	mu.Lock()
	defer mu.Unlock()
	if quiet {
		return
	}
	fmt.Fprint(os.Stderr, "\r\033[K")
}
