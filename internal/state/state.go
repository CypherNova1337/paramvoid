// Package state provides resumable-scan checkpointing. Progress is written
// atomically to a JSON file so that an interrupted or rate-limited run can be
// resumed with --resume instead of starting over. For multi-URL scans, completed
// URLs are skipped; for a single large scan, the pending chunk queue is saved so
// narrowing continues where it left off.
package state

import (
	"encoding/json"
	"os"
	"sync"
	"time"
)

// URLState is the saved progress for one target URL.
type URLState struct {
	Done       bool       `json:"done"`
	Params     []string   `json:"params,omitempty"`     // final confirmed params (when Done)
	Pending    [][]string `json:"pending,omitempty"`    // chunk queue still to narrow (in-progress)
	Candidates []string   `json:"candidates,omitempty"` // singles awaiting verification
	Method     string     `json:"method,omitempty"`
}

// State is the on-disk checkpoint. It is safe for concurrent use.
type State struct {
	mu       sync.Mutex
	path     string
	lastSave time.Time

	URLs    map[string]*URLState `json:"urls"`
	Version int                  `json:"version"`
}

// Load opens an existing state file, or returns a fresh state if it does not
// exist (or if resume is false). path == "" disables checkpointing entirely
// (returns nil, nil).
func Load(path string, resume bool) (*State, error) {
	if path == "" {
		return nil, nil
	}
	s := &State{path: path, URLs: map[string]*URLState{}, Version: 1}
	if !resume {
		return s, nil
	}
	b, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return s, nil
		}
		return nil, err
	}
	if err := json.Unmarshal(b, s); err != nil {
		return nil, err
	}
	if s.URLs == nil {
		s.URLs = map[string]*URLState{}
	}
	s.path = path
	return s, nil
}

// Get returns the saved state for a URL, or nil if none.
func (s *State) Get(url string) *URLState {
	if s == nil {
		return nil
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.URLs[url]
}

// IsDone reports whether a URL has already been fully scanned.
func (s *State) IsDone(url string) bool {
	st := s.Get(url)
	return st != nil && st.Done
}

// SaveProgress records the in-progress queue/candidates for a URL and flushes to
// disk (throttled to avoid excessive writes). force writes immediately.
func (s *State) SaveProgress(url, method string, pending [][]string, candidates []string, force bool) {
	if s == nil {
		return
	}
	s.mu.Lock()
	st := s.URLs[url]
	if st == nil {
		st = &URLState{}
		s.URLs[url] = st
	}
	st.Method = method
	st.Pending = pending
	st.Candidates = candidates
	st.Done = false
	throttled := !force && time.Since(s.lastSave) < 2*time.Second
	s.mu.Unlock()

	if !throttled {
		s.flush()
	}
}

// MarkDone records final params for a URL and flushes immediately.
func (s *State) MarkDone(url, method string, params []string) {
	if s == nil {
		return
	}
	s.mu.Lock()
	s.URLs[url] = &URLState{Done: true, Params: params, Method: method}
	s.mu.Unlock()
	s.flush()
}

// flush writes the state atomically (temp file + rename).
func (s *State) flush() {
	if s == nil || s.path == "" {
		return
	}
	s.mu.Lock()
	s.lastSave = time.Now()
	b, err := json.MarshalIndent(s, "", "  ")
	path := s.path
	s.mu.Unlock()
	if err != nil {
		return
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, b, 0o600); err != nil {
		return
	}
	_ = os.Rename(tmp, path)
}

// Flush forces a synchronous write (call on shutdown).
func (s *State) Flush() { s.flush() }

// Remove deletes the state file (call after a fully successful run).
func (s *State) Remove() {
	if s == nil || s.path == "" {
		return
	}
	_ = os.Remove(s.path)
}
