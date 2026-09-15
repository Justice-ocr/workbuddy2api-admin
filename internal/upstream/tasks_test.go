package upstream

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"workbuddy2api/internal/auth"
)

func TestReadTasksGlobalReadOnly(t *testing.T) {
	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != "GET" || r.URL.Path != "/v2/activity/growth/tasks" {
			t.Error("unexpected write or endpoint")
		}
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"code":0,"data":{"tasks":[{"task_code":"daily","name":"测试","accept_status":"claimed","progress":{"current":1,"target":1},"refresh_token":"never-expose"}]}}`))
	}))
	defer s.Close()
	c := New()
	c.GlobalEnabled = true
	c.ChatBaseGlobal = s.URL
	c.ChatBaseCN = "http://127.0.0.1:1"
	a, err := auth.Parse([]byte(`{"accessToken":"a","realm":"global","uid":"u"}`))
	if err != nil {
		t.Fatal(err)
	}
	tasks, err := c.ReadTasks(a)
	if err != nil || len(tasks) != 1 {
		t.Fatal(err)
	}
	raw, _ := json.Marshal(tasks)
	var obj []map[string]any
	json.Unmarshal(raw, &obj)
	if _, ok := obj[0]["refresh_token"]; ok {
		t.Fatal("unapproved field exposed")
	}
}
