package server

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
	"workbuddy2api/internal/auth"
	"workbuddy2api/internal/pool"
)

func TestUsageStreamAndAggregate(t *testing.T) {
	for _, stream := range []bool{false, true} {
		p := pool.New("")
		p.Add(&auth.Auth{UID: "u", AccessToken: "fake", ExpiresAt: time.Now().Add(time.Hour).Unix()})
		up := newFakeUpstream(t, func(string) (int, string, bool) {
			return 200, strings.Replace(sseOK, `"total_tokens":2`, `"total_tokens":2,"credit":0`, 1), true
		})
		var records []UsageRecord
		h := NewHandler(Config{Pool: p, Upstream: up, RecordUsage: func(r UsageRecord) { records = append(records, r) }})
		body, _ := json.Marshal(map[string]any{"model": "glm-5.2", "stream": stream, "messages": []any{map[string]any{"role": "user", "content": "PRIVATE-CONTENT"}}})
		w := httptest.NewRecorder()
		h.ServeHTTP(w, httptest.NewRequest(http.MethodPost, "/v1/chat/completions", strings.NewReader(string(body))))
		p.Close()
		if w.Code != 200 || len(records) != 1 {
			t.Fatal("request not recorded", w.Code, len(records))
		}
		r := records[0]
		if r.Input == nil || *r.Input != 1 || r.Output == nil || *r.Output != 1 || r.Credit == nil || *r.Credit != 0 {
			t.Fatalf("bad usage %#v", r)
		}
		raw, _ := json.Marshal(r)
		if strings.Contains(string(raw), "PRIVATE-CONTENT") {
			t.Fatal("content leaked")
		}
	}
}
func TestUsagePartialTokenUnknown(t *testing.T) {
	s := newChatStatsReaderSince(strings.NewReader(""), time.Now())
	s.parseSSELine(`data: {"usage":{"completion_tokens":0,"credit":0}}`)
	if s.inputCount != nil || s.outputCount == nil || *s.outputCount != 0 {
		t.Fatal("missing tokens became zero")
	}
}

func TestUsagePersistenceAndRetention(t *testing.T) {
	path := filepath.Join(t.TempDir(), "usage.json")
	s, err := NewUsageStore(path)
	if err != nil {
		t.Fatal(err)
	}
	zero := 0.0
	s.Add(UsageRecord{Time: time.Now().AddDate(0, 0, -31), Model: "expired"})
	s.Add(UsageRecord{Time: time.Now(), Model: "global:free", Credit: &zero})
	s.Add(UsageRecord{Time: time.Now(), Model: "global:unknown"})
	s.Close()
	reloaded, err := NewUsageStore(path)
	if err != nil {
		t.Fatal(err)
	}
	defer reloaded.Close()
	rows, ok := reloaded.List()
	if !ok || len(rows) != 2 || rows[0].Credit == nil || rows[1].Credit != nil {
		t.Fatal("retention/null semantics lost")
	}
}
func TestUsageWriteFailureVisible(t *testing.T) {
	path := filepath.Join(t.TempDir(), "usage.json")
	s, err := NewUsageStore(path)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	if err = os.Mkdir(path, 0o700); err != nil {
		t.Fatal(err)
	}
	s.Add(UsageRecord{Time: time.Now()})
	s.Flush()
	_, ok := s.List()
	if ok {
		t.Fatal("write error hidden")
	}
}
