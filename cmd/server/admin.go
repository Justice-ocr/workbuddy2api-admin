package main

import (
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"embed"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"strconv"
	"strings"
	"sync"
	"time"

	"workbuddy2api/internal/auth"
	"workbuddy2api/internal/oauth"
	"workbuddy2api/internal/pool"
	"workbuddy2api/internal/server"
	"workbuddy2api/internal/upstream"
)

//go:embed admin.html admin.css admin-theme.css admin.js admin-icons.js
var adminAssets embed.FS

type loginSession struct {
	realm             string
	state             string
	client            *http.Client
	created, nextPoll time.Time
	token             *oauth.Token
}
type confirmation struct {
	UID     string
	Expires time.Time
}
type adminEvent struct {
	Time   string `json:"time"`
	Action string `json:"action"`
	Result string `json:"result"`
}
type adminServer struct {
	usage               *server.UsageStore
	pool                *pool.Pool
	up                  *upstream.Client
	api                 *server.Handler
	authDir, configPath string
	digest              [32]byte
	mu                  sync.Mutex
	eventMu             sync.Mutex
	logins              map[string]*loginSession
	confirms            map[string]confirmation
	events              []adminEvent
	started             time.Time
	failureWindow       time.Time
	failures            int
	oauthClient         func() *http.Client
}

func newAdminServer(p *pool.Pool, up *upstream.Client, api *server.Handler, dir, configPath, token string) *adminServer {
	return &adminServer{pool: p, up: up, api: api, authDir: dir, configPath: configPath,
		digest: sha256.Sum256([]byte(token)), logins: map[string]*loginSession{},
		confirms: map[string]confirmation{}, events: []adminEvent{}, started: time.Now(), oauthClient: oauth.NewClient}
}

func randomID() string {
	var b [32]byte
	if _, err := rand.Read(b[:]); err != nil {
		panic("secure random unavailable")
	}
	return hex.EncodeToString(b[:])
}

func loopbackHost(host string) bool {
	name, port, err := net.SplitHostPort(host)
	if err != nil {
		return false
	}
	n, err := strconv.Atoi(port)
	if err != nil || n < 1 || n > 65535 {
		return false
	}
	ip := net.ParseIP(name)
	return name == "localhost" || ip != nil && ip.IsLoopback()
}

func (a *adminServer) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Security-Policy", "default-src 'none'; script-src 'self'; style-src 'self'; img-src 'self' data:; connect-src 'self'; base-uri 'none'; frame-ancestors 'none'; form-action 'self'")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.Header().Set("X-Frame-Options", "DENY")
	w.Header().Set("Referrer-Policy", "no-referrer")
	w.Header().Set("Cache-Control", "no-store")
	// Prevent DNS rebinding. Remote access is intentionally unsupported.
	if !loopbackHost(r.Host) {
		adminError(w, 403, "管理入口仅允许本机访问")
		return
	}
	if origin := r.Header.Get("Origin"); origin != "" {
		u, err := url.Parse(origin)
		scheme := "http"
		if r.TLS != nil {
			scheme = "https"
		}
		if err != nil || u.Scheme != scheme || u.Host != r.Host || u.User != nil || u.Path != "" || u.RawQuery != "" || u.Fragment != "" {
			adminError(w, 403, "已拒绝跨站请求")
			return
		}
	}
	if r.Header.Get("Sec-Fetch-Site") == "cross-site" {
		adminError(w, 403, "已拒绝跨站请求")
		return
	}
	if r.Method == http.MethodGet {
		file, contentType := "", ""
		switch r.URL.Path {
		case "/":
			file = "admin.html"
			contentType = "text/html; charset=utf-8"
		case "/admin.css":
			file = "admin.css"
			contentType = "text/css; charset=utf-8"
		case "/admin-theme.css":
			file = "admin-theme.css"
			contentType = "text/css; charset=utf-8"
		case "/admin.js":
			file = "admin.js"
			contentType = "text/javascript; charset=utf-8"
		case "/admin-icons.js":
			file = "admin-icons.js"
			contentType = "text/javascript; charset=utf-8"
		}
		if file != "" {
			raw, _ := adminAssets.ReadFile(file)
			w.Header().Set("Content-Type", contentType)
			_, _ = w.Write(raw)
			return
		}
	}
	if !strings.HasPrefix(r.URL.Path, "/api/") {
		http.NotFound(w, r)
		return
	}
	bearer := r.Header.Get("Authorization")
	got := sha256.Sum256([]byte(strings.TrimPrefix(bearer, "Bearer ")))
	empty := sha256.Sum256(nil)
	if a.digest == empty || !strings.HasPrefix(bearer, "Bearer ") || subtle.ConstantTimeCompare(got[:], a.digest[:]) != 1 {
		a.mu.Lock()
		if time.Since(a.failureWindow) > time.Minute {
			a.failureWindow = time.Now()
			a.failures = 0
		}
		a.failures++
		limited := a.failures > 30
		a.mu.Unlock()
		if limited {
			w.Header().Set("Retry-After", "60")
			adminError(w, 429, "认证尝试过多，请稍后重试")
		} else {
			adminError(w, 401, "管理凭证无效")
		}
		return
	}
	// No cookies or CORS. A non-simple header is required even for reads.
	if r.Header.Get("X-Admin-Request") != "1" {
		adminError(w, 403, "缺少管理请求标识")
		return
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	switch {
	case r.Method == "GET" && r.URL.Path == "/api/usage":
		a.usageList(w, r)
	case r.Method == "POST" && r.URL.Path == "/api/tasks":
		a.taskStatus(w, r)
	case r.Method == "GET" && r.URL.Path == "/api/overview":
		total, healthy, cooling, disabled, _ := a.pool.CountsDetailed()
		adminJSON(w, map[string]any{"accounts": a.pool.List(), "total": total, "healthy": healthy, "cooling": cooling,
			"disabled": disabled, "uptime_seconds": int(time.Since(a.started).Seconds()), "global_enabled": auth.GlobalEnabled(),
			"api_key_configured": a.api.APIKeyConfigured(), "api_key_editable": os.Getenv("WB2A_API_KEY") == ""})
	case r.Method == "GET" && r.URL.Path == "/api/models":
		adminJSON(w, map[string]any{"data": a.api.Models()})
	case r.Method == "GET" && r.URL.Path == "/api/logs":
		a.eventMu.Lock()
		events := append([]adminEvent{}, a.events...)
		a.eventMu.Unlock()
		adminJSON(w, map[string]any{"entries": events})
	case r.Method == "POST" && r.URL.Path == "/api/login/start":
		a.loginStart(w, r)
	case r.Method == "POST" && r.URL.Path == "/api/login/poll":
		a.loginPoll(w, r)
	case r.Method == "POST" && r.URL.Path == "/api/login/cancel":
		a.loginCancel(w, r)
	case r.Method == "POST" && r.URL.Path == "/api/accounts/action":
		a.accountAction(w, r)
	case r.Method == "POST" && r.URL.Path == "/api/api-key":
		a.rotateKey(w, r)
	default:
		http.NotFound(w, r)
	}
}

func decodeAdmin(w http.ResponseWriter, r *http.Request, v any) bool {
	if strings.Split(r.Header.Get("Content-Type"), ";")[0] != "application/json" {
		adminError(w, 415, "请求必须为 JSON")
		return false
	}
	r.Body = http.MaxBytesReader(w, r.Body, 8192)
	d := json.NewDecoder(r.Body)
	d.DisallowUnknownFields()
	if err := d.Decode(v); err != nil {
		adminError(w, 400, "请求格式无效")
		return false
	}
	if err := d.Decode(new(any)); !errors.Is(err, io.EOF) {
		adminError(w, 400, "请求格式无效")
		return false
	}
	return true
}

func adminJSON(w http.ResponseWriter, v any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	_ = json.NewEncoder(w).Encode(v)
}
func adminError(w http.ResponseWriter, status int, msg string) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(map[string]string{"error": msg})
}
func (a *adminServer) event(action, result string) {
	a.eventMu.Lock()
	defer a.eventMu.Unlock()
	// Only constant labels enter this buffer, never upstream bodies or credentials.
	a.events = append(a.events, adminEvent{time.Now().Format(time.RFC3339), action, result})
	if len(a.events) > 200 {
		a.events = append([]adminEvent(nil), a.events[len(a.events)-200:]...)
	}
}
