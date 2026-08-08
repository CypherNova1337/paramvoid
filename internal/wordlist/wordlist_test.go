package wordlist

import (
	"os"
	"path/filepath"
	"testing"
)

func TestEmbeddedDefaultLoads(t *testing.T) {
	words, err := Load("default")
	if err != nil {
		t.Fatalf("Load default: %v", err)
	}
	if len(words) < 1000 {
		t.Fatalf("embedded wordlist unexpectedly small: %d", len(words))
	}
}

func TestLoadSkipsBlanksCommentsAndDedupes(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "wl.txt")
	content := "user_id\n\n# a comment\n  page  \nuser_id\n#another\ndebug\n"
	if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	words, err := Load(p)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	want := []string{"user_id", "page", "debug"}
	if len(words) != len(want) {
		t.Fatalf("got %v, want %v", words, want)
	}
	for i := range want {
		if words[i] != want[i] {
			t.Fatalf("order/content mismatch: got %v, want %v", words, want)
		}
	}
}

func TestDetectStyle(t *testing.T) {
	cases := map[string]Style{
		"like_this": Snake,
		"LIKE_THIS": Screaming,
		"like-this": Kebab,
		"LikeThis":  Pascal,
		"likeThis":  Camel,
		"likethis":  Flat,
	}
	for in, want := range cases {
		if got := DetectStyle(in); got != want {
			t.Errorf("DetectStyle(%q) = %v, want %v", in, got, want)
		}
	}
}

func TestApplyCasing(t *testing.T) {
	in := []string{"user_id", "apiKey", "X-Forwarded-For", "debug"}
	cases := []struct {
		style Style
		want  []string
	}{
		{Snake, []string{"user_id", "api_key", "x_forwarded_for", "debug"}},
		{Camel, []string{"userId", "apiKey", "xForwardedFor", "debug"}},
		{Pascal, []string{"UserId", "ApiKey", "XForwardedFor", "Debug"}},
		{Kebab, []string{"user-id", "api-key", "x-forwarded-for", "debug"}},
		{Flat, []string{"userid", "apikey", "xforwardedfor", "debug"}},
		{Screaming, []string{"USER_ID", "API_KEY", "X_FORWARDED_FOR", "DEBUG"}},
	}
	for _, c := range cases {
		got := Apply(in, c.style)
		for i := range c.want {
			if got[i] != c.want[i] {
				t.Errorf("Apply(%v, %v)[%d] = %q, want %q", in, c.style, i, got[i], c.want[i])
			}
		}
	}
}
