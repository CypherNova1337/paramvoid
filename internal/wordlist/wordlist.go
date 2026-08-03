// Package wordlist loads parameter-name wordlists (from the embedded default or
// a user file) and optionally rewrites every entry into a requested casing
// style so a single list can be reused across naming conventions.
package wordlist

import (
	_ "embed"
	"os"
	"strings"
	"unicode"
)

//go:embed default.txt
var defaultList string

// Load reads a wordlist. If path is "" or "default", the embedded list is used.
// Blank lines and lines beginning with '#' are ignored; entries are de-duplicated
// while preserving first-seen order.
func Load(path string) ([]string, error) {
	raw := defaultList
	if path != "" && path != "default" {
		b, err := os.ReadFile(path)
		if err != nil {
			return nil, err
		}
		raw = string(b)
	}

	seen := make(map[string]bool)
	var out []string
	for _, line := range strings.Split(raw, "\n") {
		w := strings.TrimSpace(line)
		if w == "" || strings.HasPrefix(w, "#") {
			continue
		}
		if !seen[w] {
			seen[w] = true
			out = append(out, w)
		}
	}
	return out, nil
}

// Style enumerates the supported casing conventions.
type Style int

const (
	Snake     Style = iota // like_this
	Kebab                  // like-this
	Camel                  // likeThis
	Pascal                 // LikeThis
	Flat                   // likethis
	Screaming              // LIKE_THIS
)

// DetectStyle infers a casing style from a user-supplied example token.
func DetectStyle(example string) Style {
	switch {
	case strings.Contains(example, "_"):
		if example == strings.ToUpper(example) {
			return Screaming
		}
		return Snake
	case strings.Contains(example, "-"):
		return Kebab
	case example != "" && unicode.IsUpper(rune(example[0])) && strings.ToLower(example) != example:
		return Pascal
	case strings.ToLower(example) != example:
		return Camel
	default:
		return Flat
	}
}

// Apply rewrites every word into the given style. Words are first split into
// lowercase tokens on delimiters and camelCase boundaries, then re-joined.
func Apply(words []string, style Style) []string {
	out := make([]string, 0, len(words))
	for _, w := range words {
		out = append(out, render(tokenize(w), style))
	}
	return out
}

// tokenize breaks a word into lowercase tokens on non-alphanumeric characters
// and camelCase / PascalCase boundaries.
func tokenize(w string) []string {
	// Split on explicit delimiters first.
	fields := strings.FieldsFunc(w, func(r rune) bool {
		return !unicode.IsLetter(r) && !unicode.IsDigit(r)
	})
	var tokens []string
	for _, f := range fields {
		tokens = append(tokens, splitCamel(f)...)
	}
	for i := range tokens {
		tokens[i] = strings.ToLower(tokens[i])
	}
	if len(tokens) == 0 {
		return []string{strings.ToLower(w)}
	}
	return tokens
}

func splitCamel(s string) []string {
	var (
		parts []string
		cur   strings.Builder
	)
	runes := []rune(s)
	for i, r := range runes {
		if i > 0 && unicode.IsUpper(r) && (unicode.IsLower(runes[i-1]) || unicode.IsDigit(runes[i-1])) {
			parts = append(parts, cur.String())
			cur.Reset()
		}
		cur.WriteRune(r)
	}
	if cur.Len() > 0 {
		parts = append(parts, cur.String())
	}
	return parts
}

func render(tokens []string, style Style) string {
	switch style {
	case Snake:
		return strings.Join(tokens, "_")
	case Screaming:
		return strings.ToUpper(strings.Join(tokens, "_"))
	case Kebab:
		return strings.Join(tokens, "-")
	case Flat:
		return strings.Join(tokens, "")
	case Pascal:
		return joinTitle(tokens, true)
	case Camel:
		return joinTitle(tokens, false)
	default:
		return strings.Join(tokens, "_")
	}
}

func joinTitle(tokens []string, upperFirst bool) string {
	var b strings.Builder
	for i, t := range tokens {
		if t == "" {
			continue
		}
		if i == 0 && !upperFirst {
			b.WriteString(t)
			continue
		}
		b.WriteString(strings.ToUpper(t[:1]) + t[1:])
	}
	return b.String()
}
