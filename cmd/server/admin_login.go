package main

import (
	"encoding/json"
	"errors"
	"net/http"
	"os"
	"path/filepath"
	"time"

	"workbuddy2api/internal/auth"
	"workbuddy2api/internal/oauth"
)

func (a *adminServer) pruneLogins() {
	for id, s := range a.logins {
		if time.Since(s.created) > 15*time.Minute {
			delete(a.logins, id)
		}
	}
}
func (a *adminServer) loginStart(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Realm string `json:"realm"`
	}
	if !decodeAdmin(w, r, &req) {
		return
	}
	if req.Realm != "global" || !auth.GlobalEnabled() {
		adminError(w, 400, "仅支持已启用的国际版 global 登录")
		return
	}
	a.pruneLogins()
	if len(a.logins) >= 8 {
		adminError(w, 429, "待授权会话过多，请关闭旧会话")
		return
	}
	client := a.oauthClient()
	st, err := oauth.Begin(r.Context(), client, oauth.GlobalBase, oauth.GlobalBase)
	if err != nil || !oauth.ValidGlobalURL(st.AuthURL) {
		a.event("国际版授权", "发起失败")
		adminError(w, 502, "国际版授权入口不可用")
		return
	}
	id := randomID()
	a.logins[id] = &loginSession{state: st.State, client: client, created: time.Now()}
	a.event("国际版授权", "等待授权")
	adminJSON(w, map[string]any{"id": id, "url": st.AuthURL, "realm": "global", "expires_in": 900})
}
func (a *adminServer) loginCancel(w http.ResponseWriter, r *http.Request) {
	var req struct {
		ID string `json:"id"`
	}
	if !decodeAdmin(w, r, &req) {
		return
	}
	delete(a.logins, req.ID)
	adminJSON(w, map[string]bool{"ok": true})
}
func (a *adminServer) loginPoll(w http.ResponseWriter, r *http.Request) {
	var req struct {
		ID string `json:"id"`
	}
	if !decodeAdmin(w, r, &req) {
		return
	}
	a.pruneLogins()
	s := a.logins[req.ID]
	if s == nil {
		adminError(w, 410, "授权已过期或取消，请重新添加")
		return
	}
	if !auth.GlobalEnabled() {
		adminError(w, 409, "国际版路由已关闭")
		return
	}
	if time.Now().Before(s.nextPoll) {
		adminError(w, 429, "请稍后查询授权状态")
		return
	}
	s.nextPoll = time.Now().Add(2 * time.Second)
	if s.token == nil {
		tok, err := oauth.PollToken(r.Context(), s.client, oauth.GlobalBase, oauth.GlobalBase, s.state)
		if errors.Is(err, oauth.ErrPending) {
			adminJSON(w, map[string]bool{"done": false})
			return
		}
		if err != nil {
			adminError(w, 502, "授权状态暂时不可用")
			return
		}
		if tok.RefreshToken == "" || tok.ExpiresIn <= 0 || tok.ExpiresIn > 365*24*3600 {
			delete(a.logins, req.ID)
			adminError(w, 502, "上游未返回有效的可刷新凭据")
			return
		}
		s.token = &tok
	}
	tok := s.token
	info, err := oauth.ReadAccount(r.Context(), s.client, oauth.GlobalBase, oauth.GlobalBase, s.state, tok.AccessToken)
	if err != nil || !validAdminUID(info.UID) {
		adminError(w, 502, "账号信息获取失败")
		return
	}
	if a.pool.AuthByUID(info.UID) != nil {
		delete(a.logins, req.ID)
		adminError(w, 409, "账号已存在，原凭据未被覆盖")
		return
	}
	if err := os.MkdirAll(a.authDir, 0o700); err != nil {
		adminError(w, 500, "账号目录不可写")
		return
	}
	path := filepath.Join(a.authDir, "workbuddy-"+info.UID+".json")
	if !ownedPath(a.authDir, path, true) {
		adminError(w, 409, "账号文件已存在或路径不安全")
		return
	}
	raw, _ := json.Marshal(map[string]any{
		"auth": map[string]any{"accessToken": tok.AccessToken, "refreshToken": tok.RefreshToken,
			"expiresAt": time.Now().Unix() + tok.ExpiresIn, "domain": tok.Domain, "realm": "global"},
		"account": info,
	})
	if err := auth.CreateOnly(path, raw); err != nil {
		adminError(w, 409, "凭据保存失败，未替换任何现有文件")
		return
	}
	acct, _ := auth.Parse(raw)
	acct.FilePath = path
	a.pool.Add(acct)
	a.pool.Flush()
	delete(a.logins, req.ID)
	a.event("国际版账号", "添加成功")
	adminJSON(w, map[string]any{"done": true, "uid": info.UID, "nickname": info.Nickname, "realm": "global"})
}

func validAdminUID(s string) bool {
	if s == "" || len(s) > 64 {
		return false
	}
	for _, c := range s {
		if !(c == '-' || c == '_' || c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9') {
			return false
		}
	}
	return true
}
