package server

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"sync"
	"time"
)

type UsageRecord struct {
	Time        time.Time `json:"time"`
	Model       string    `json:"model"`
	UID         string    `json:"uid"`
	Mode        string    `json:"mode"`
	Status      int       `json:"status"`
	DurationMS  int64     `json:"duration_ms"`
	TTFBMS      int64     `json:"ttfb_ms"`
	Input       *int      `json:"input_tokens"`
	Output      *int      `json:"output_tokens"`
	Credit      *float64  `json:"credit"`
	Interrupted bool      `json:"interrupted"`
}

// UsageStore retains bounded metadata only. No messages, headers or raw errors are stored.
type UsageStore struct {
	mu      sync.Mutex
	writeMu sync.Mutex
	path    string
	rows    []UsageRecord
	failed  bool
	dirty   bool
	stop    chan struct{}
	done    chan struct{}
	once    sync.Once
	version uint64
}

func NewUsageStore(path string) (*UsageStore, error) {
	s := &UsageStore{path: path, rows: []UsageRecord{}, stop: make(chan struct{}), done: make(chan struct{})}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return nil, err
	}
	if st, err := os.Lstat(path); err == nil {
		if !st.Mode().IsRegular() || st.Size() > 16<<20 {
			return nil, errors.New("invalid usage metadata file")
		}
	}
	raw, err := os.ReadFile(path)
	if err != nil && !os.IsNotExist(err) {
		return nil, err
	}
	if err == nil {
		if err = json.Unmarshal(raw, &s.rows); err != nil {
			return nil, err
		}
	}
	s.prune()
	s.dirty = true
	go func() {
		defer close(s.done)
		t := time.NewTicker(2 * time.Second)
		defer t.Stop()
		for {
			select {
			case <-t.C:
				s.Flush()
			case <-s.stop:
				s.Flush()
				return
			}
		}
	}()
	return s, nil
}
func (s *UsageStore) Close() { s.once.Do(func() { close(s.stop) }); <-s.done }
func (s *UsageStore) prune() {
	before := len(s.rows)
	cut := time.Now().AddDate(0, 0, -30)
	out := s.rows[:0]
	for _, r := range s.rows {
		if r.Time.After(cut) {
			out = append(out, r)
		}
	}
	s.rows = out
	if len(s.rows) > 5000 {
		s.rows = append([]UsageRecord(nil), s.rows[len(s.rows)-5000:]...)
	}
	if len(s.rows) != before {
		s.dirty = true
		s.version++
	}
}
func (s *UsageStore) Add(r UsageRecord) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.rows = append(s.rows, r)
	s.prune()
	s.dirty = true
	s.version++
}
func (s *UsageStore) Flush() {
	s.writeMu.Lock()
	defer s.writeMu.Unlock()
	s.mu.Lock()
	s.prune()
	if !s.dirty {
		s.mu.Unlock()
		return
	}
	rows := append([]UsageRecord{}, s.rows...)
	version := s.version
	s.mu.Unlock()
	mark := func(err error) {
		s.mu.Lock()
		defer s.mu.Unlock()
		s.failed = err != nil
		if err == nil && s.version == version {
			s.dirty = false
		}
	}
	raw, err := json.Marshal(rows)
	if err != nil {
		mark(err)
		return
	}
	f, err := os.CreateTemp(filepath.Dir(s.path), ".usage-*")
	if err != nil {
		mark(err)
		return
	}
	defer os.Remove(f.Name())
	if err = f.Chmod(0o600); err == nil {
		_, err = f.Write(raw)
	}
	if err == nil {
		err = f.Sync()
	}
	closeErr := f.Close()
	if err == nil {
		err = closeErr
	}
	if err == nil {
		err = os.Rename(f.Name(), s.path)
	}
	mark(err)
}
func (s *UsageStore) List() ([]UsageRecord, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.prune()
	return append([]UsageRecord{}, s.rows...), !s.failed
}
