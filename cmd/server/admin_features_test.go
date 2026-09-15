package main

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"workbuddy2api/internal/auth"
	"workbuddy2api/internal/oauth"
	"workbuddy2api/internal/pool"
	"workbuddy2api/internal/server"
)

type featureTransport func(*http.Request) (*http.Response, error)

func (f featureTransport) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }
func featureCall(a *adminServer, path, body string) *httptest.ResponseRecorder {
	r := httptest.NewRequest("POST", "http://127.0.0.1:7864/api/"+path, strings.NewReader(body))
	r.Header.Set("Content-Type", "application/json")
	r.Header.Set("Authorization", "Bearer "+strings.Repeat("a", 32))
	r.Header.Set("X-Admin-Request", "1")
	w := httptest.NewRecorder()
	a.ServeHTTP(w, r)
	return w
}
func TestLoginRealmIsolation(t *testing.T) {
	for _, realm := range []string{"cn", "global"} {
		t.Run(realm, func(t *testing.T) {
			p := pool.New("")
			defer p.Close()
			dir := t.TempDir()
			a := newAdminServer(p, nil, nil, dir, "", strings.Repeat("a", 32))
			base, origin := loginEndpoints(realm)
			url := base + "/authorize?state=state"
			a.oauthClient = func() *http.Client {
				return &http.Client{Transport: featureTransport(func(r *http.Request) (*http.Response, error) {
					if r.URL.Scheme+"://"+r.URL.Host != base || r.Header.Get("Origin") != origin {
						t.Fatal("cross-realm request")
					}
					var data any
					switch r.URL.Path {
					case "/v2/plugin/auth/state":
						data = map[string]any{"state": "state", "authUrl": url}
					case "/v2/plugin/auth/token":
						data = map[string]any{"accessToken": "test-access", "refreshToken": "test-refresh", "expiresIn": 3600, "domain": strings.TrimPrefix(base, "https://")}
					case "/v2/plugin/login/account":
						data = map[string]any{"uid": "test-user", "nickname": "测试账号"}
					default:
						t.Fatal("unexpected endpoint")
					}
					raw, _ := json.Marshal(map[string]any{"code": 0, "data": data})
					return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader(string(raw))), Header: make(http.Header)}, nil
				})}
			}
			start := featureCall(a, "login/start", `{"realm":"`+realm+`"}`)
			if start.Code != 200 {
				t.Fatal(start.Code, start.Body.String())
			}
			var st struct {
				ID string `json:"id"`
			}
			json.Unmarshal(start.Body.Bytes(), &st)
			finish := featureCall(a, "login/poll", `{"id":"`+st.ID+`"}`)
			if finish.Code != 200 {
				t.Fatal(finish.Code, finish.Body.String())
			}
			if strings.Contains(finish.Body.String(), "test-access") || strings.Contains(finish.Body.String(), "test-refresh") {
				t.Fatal("token exposed")
			}
			raw, err := os.ReadFile(filepath.Join(dir, "workbuddy-test-user.json"))
			if err != nil {
				t.Fatal(err)
			}
			account, err := auth.Parse(raw)
			if err != nil || account.RealmStored() != realm {
				t.Fatal("wrong stored realm", err)
			}
			// A second successful authorization must not overwrite the existing file.
			start = featureCall(a, "login/start", `{"realm":"`+realm+`"}`)
			json.Unmarshal(start.Body.Bytes(), &st)
			finish = featureCall(a, "login/poll", `{"id":"`+st.ID+`"}`)
			if finish.Code != 409 {
				t.Fatal("duplicate was accepted")
			}
			after, _ := os.ReadFile(filepath.Join(dir, "workbuddy-test-user.json"))
			if string(raw) != string(after) {
				t.Fatal("existing credentials changed")
			}
		})
	}
}
func TestLoginURLAllowlist(t *testing.T) {
	for _, v := range []struct {
		realm, url string
		want       bool
	}{
		{"global", oauth.GlobalBase + "/login", true}, {"cn", oauth.CNBase + "/login", true},
		{"cn", oauth.CNOrigin + "/login", true}, {"global", oauth.CNBase + "/login", false},
		{"cn", oauth.GlobalBase + "/login", false}, {"cn", "https://copilot.tencent.com.evil.test", false},
		{"cn", "https://user@copilot.tencent.com", false}, {"global", "http://www.workbuddy.ai", false},
	} {
		if validLoginURL(v.realm, v.url) != v.want {
			t.Errorf("unexpected acceptance %s", v.url)
		}
	}
}
func TestUsageEndpointAuthAndPagination(t *testing.T) {
	store, err := server.NewUsageStore(filepath.Join(t.TempDir(), "usage.json"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	for i := 0; i < 55; i++ {
		store.Add(server.UsageRecord{Time: time.Now(), Model: "global:test", UID: "u", Status: 200})
	}
	a := newAdminServer(nil, nil, nil, "", "", strings.Repeat("a", 32))
	a.usage = store
	r := httptest.NewRequest("GET", "http://127.0.0.1:7864/api/usage?page=2", nil)
	w := httptest.NewRecorder()
	a.ServeHTTP(w, r)
	if w.Code != 401 {
		t.Fatal("usage not protected")
	}
	r.Header.Set("Authorization", "Bearer "+strings.Repeat("a", 32))
	r.Header.Set("X-Admin-Request", "1")
	w = httptest.NewRecorder()
	a.ServeHTTP(w, r)
	var out struct {
		Rows  []server.UsageRecord `json:"rows"`
		Total int                  `json:"total"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &out); err != nil {
		t.Fatal(err)
	}
	if len(out.Rows) != 5 || out.Total != 55 {
		t.Fatal("bad pagination", out.Total, len(out.Rows))
	}
}
