package finding

import "testing"

func TestForReason(t *testing.T) {
	cases := map[string]Confidence{
		"http-code":   High,
		"redirect":    High,
		"reflection":  Low,
		"body-length": Medium,
		"word-count":  Medium,
		"line-count":  Medium,
		"headers":     Medium,
		"":            Medium,
	}
	for reason, want := range cases {
		if got := ForReason(reason); got != want {
			t.Errorf("ForReason(%q) = %q, want %q", reason, got, want)
		}
	}
}

func TestNoteDedup(t *testing.T) {
	f := Finding{Param: "x"}
	f.Note("static-asset")
	f.Note("static-asset")
	f.Note("site-wide")
	if len(f.Notes) != 2 {
		t.Fatalf("notes = %v, want 2 distinct", f.Notes)
	}
}

func TestNames(t *testing.T) {
	fs := []Finding{{Param: "a"}, {Param: "b"}, {Param: "c"}}
	got := Names(fs)
	want := []string{"a", "b", "c"}
	if len(got) != 3 || got[0] != "a" || got[2] != "c" {
		t.Fatalf("Names = %v, want %v", got, want)
	}
}
