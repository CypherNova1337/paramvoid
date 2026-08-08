package state

import (
	"path/filepath"
	"testing"
)

func TestNilStateIsSafe(t *testing.T) {
	var s *State
	// All methods must tolerate a nil (checkpointing-disabled) receiver.
	if s.IsDone("x") {
		t.Fatal("nil state should report not done")
	}
	if s.Get("x") != nil {
		t.Fatal("nil state Get should be nil")
	}
	s.SaveProgress("x", "GET", nil, nil, true)
	s.MarkDone("x", "GET", nil)
	s.Flush()
	s.Remove()
}

func TestSaveLoadRoundTrip(t *testing.T) {
	p := filepath.Join(t.TempDir(), "s.json")

	s, err := Load(p, false)
	if err != nil {
		t.Fatal(err)
	}
	s.SaveProgress("http://a/", "GET", [][]string{{"a", "b"}, {"c"}}, []string{"cand"}, true)
	s.MarkDone("http://b/", "POST", []string{"user_id", "debug"})

	// Reload with resume and verify persistence.
	s2, err := Load(p, true)
	if err != nil {
		t.Fatal(err)
	}
	if !s2.IsDone("http://b/") {
		t.Fatal("expected http://b/ to be done after reload")
	}
	done := s2.Get("http://b/")
	if done == nil || len(done.Params) != 2 || done.Params[0] != "user_id" {
		t.Fatalf("done params not persisted: %+v", done)
	}
	pend := s2.Get("http://a/")
	if pend == nil || pend.Done || len(pend.Pending) != 2 || len(pend.Candidates) != 1 {
		t.Fatalf("pending state not persisted: %+v", pend)
	}
}

func TestLoadWithoutResumeStartsFresh(t *testing.T) {
	p := filepath.Join(t.TempDir(), "s.json")
	s, _ := Load(p, false)
	s.MarkDone("http://a/", "GET", []string{"x"})

	// Without resume, a fresh Load ignores the existing file.
	s2, err := Load(p, false)
	if err != nil {
		t.Fatal(err)
	}
	if s2.IsDone("http://a/") {
		t.Fatal("without --resume the state should start empty")
	}
}

func TestLoadEmptyPathDisables(t *testing.T) {
	s, err := Load("", false)
	if err != nil {
		t.Fatal(err)
	}
	if s != nil {
		t.Fatal("empty path should return a nil (disabled) state")
	}
}
